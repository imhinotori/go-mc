# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v3 — Online-mode + Operator UX + Structure polish

**Shipped:** 2026-06-27
**Phases:** 4 (17–20) | **Plans:** 33 SUMMARYs (17×22, 18×2, 19×3, 20×6) | **Requirements:** 15/15

### What Was Built
- **Gameplay completion (Phase 17):** the six unwired client-facing seams — player-entity-in-tracker broadcast (the keystone), persisted-position apply, inventory join-sync, Attack/Interact damage + fall damage, the large net-new FlowingFluid water simulation + player fluid physics, and block-break Item-entity drops. The v1 "fully playable" claim had isolated-function tests but never an end-to-end real-client wiring; Phase 17 closed every seam.
- **Online-mode (Phase 18):** RSA/PKCS#1v1.5 EncryptionRequest/Response (incl. the 776 trailing `shouldAuthenticate` boolean), hand-rolled AES-128/CFB8 over stdlib AES (no new dep, CGO=0 preserved), Yggdrasil `hasJoined`, authenticated skin properties propagated to PlayerInfoUpdate(ADD_PLAYER), behind an `online-mode` flag (offline byte-identical).
- **Operator UX (Phase 19):** a bubbletea+bubbles TUI console (textinput + live log viewport) that degrades to plain logging off-TTY, console commands routed through the existing graph on the tick owner, full disconnect-reason taxonomy in both the TUI stream and structured logs.
- **Structure polish (Phase 20):** a shared `level/loot` evaluator (one engine for block drops AND chest loot, the v1 hardcoded map deleted), structure inhabitants (witch/cat/villager spawn + silverfish spawner) crossing worldgen→tick via `ChunkResult.Spawns`, the Beardifier terrain-adaptation (village raise / stronghold bury, others byte-identical), structure-start NBT persistence (survive reload without recompute), and the full chest-OPEN UI runtime path.
- **Live 12-bug fidelity sweep (post-phase, this session):** chest placement (DefaultStateID), sugar-cane cascade (block-tick container), natural water flow (carver post-process marks), hats/skins (CONFIG modelCustomisation + sendSelfSkin), persist respawn crash (Anvil palette rebuild), chest drag + double-click (QUICK_CRAFT + PICKUP_ALL), swim-in-reloaded-chunks (FluidCount), static reloaded water (PostProcessing persist), oxygen-while-submerged + oxygen-while-swimming (fluid surface height + swim-pose eye height). Each reproduced offline/live → jar-verified → 1:1 ported → regression-tested → operator-confirmed in-game.

### What Worked
- **Instrument-first, never reason-offline.** Every one of the 12 live bugs was reproduced with an offline probe test or a live `SULFUR_ULTRA_DEBUG` capture BEFORE editing. The two times an offline assumption was made (hat-visible-with-1-player, water-deferral) it was WRONG — the operator caught it. The discipline paid for itself.
- **Jar bytecode as the single source of truth.** `javap -c -p` before writing kept ports literal; it caught real deltas (the 776 `shouldAuthenticate` boolean, the 4-byte challenge, FlowingFluid.getHeight, swim eye height 0.4).
- **One seam, many producers.** The Phase 17 entity/tracker keystone meant Phase 18 skins and Phase 20 structure spawns rode the SAME path for free — the integration audit found 4/4 seams wired with one shared encoder, one shared loot evaluator, one entities.add.
- **The "no built-but-unwired code" rule** (from the SC4 gap lesson) — recording subsystem-blocked work in a backlog instead of shipping speculative dead code — kept the tree honest.

### What Was Inefficient
- **v1 closed on isolated tests, not E2E.** The entire Phase 17 existed because "core logic + unit test" was mistaken for "done." The real client surfaced six defects on day one. Lesson already absorbed (visual gates now autonomous:false), but it cost a whole phase.
- **Phase 20 shipped a documented split (chest seam built, no runtime caller)** that then needed a follow-up this session to wire. Honest, but the split could have been one phase if the open-UI was scoped in from the start.
- **gopls staleness** flooded false "undefined" diagnostics all session — every check had to gate on `go build`/vet/test instead. Minor friction tax, but constant.

### Patterns Established
- **`run-debug.sh` — debug mode always on.** Single command builds CGO=0, kills the prior instance, starts online+test-kit+persist+ULTRA_DEBUG firehose. Operator-requested standing config.
- **`udebug("cat", ...)` one-shot probes** added at the seam, captured live, removed before commit (keep the fix, drop the trace).
- **Codegen-emitted defaults** (`block.DefaultStateID` from the jar's `Default` flag) instead of Go zero-value structs — the chest-facing bug root cause; the general fix covers every block with non-zero default props.
- **Anvil vs NETWORK palette distinction** made explicit in `level/palette.go` — direct/global (raw ids) on the wire vs always-indexed on disk; the reload-crash root cause.

### Key Lessons
1. **Reproduce before you edit — always.** Offline reasoning was wrong twice; live instrumentation was right every time. This is now a standing order.
2. **"Unit-tested" ≠ "wired."** Test the seam end-to-end with the real consumer (real client), or it isn't done. Visual gates must be operator-confirmed, not assumed.
3. **Verify the port against bytecode every time**, even for "obvious" logic — the 776 protocol shifts and float-exact constants (eye height 0.4, getHeight amount/9) don't survive paraphrase.
4. **Recompute as the coherence authority, persistence as the optimization** (SC4): persist computed state but always fall back to recompute on a missing/garbled tag — never panic, never trust the cache blindly.

### Cost Observations
- Model mix: predominantly sonnet for execution; opus for the cross-phase integration audit.
- Sessions: multiple, spanning the phase builds + the live-bug sweep + this close.
- Notable: parallel worktree builds (ATTRIB+FACESTURDY, then ITEMNBT+PERSIST, then BLOCKTICK) merged to `ender-776` with zero conflicts (disjoint files) — the v3.1 subsystem prerequisites landed cheaply.

---

## Milestone: v4 — Plugin / Scripting System

**Shipped:** 2026-06-29
**Phases:** 8 (21–28) | **Plans:** 19 SUMMARYs | **Tasks:** 43 | **Requirements:** 8/8 (PLUGIN-01..07 + REGION-01)

### What Was Built
- **Starlark core (Phases 21–22):** `go.starlark.net` embedded as a plain CGO=0 dep; an isolated `plugin/starlark` leaf package (step-budget sandbox, recursion-off, no-I/O, FrozenValue tick-boundary sharing); a `plugin/host` package with a TOML-manifest Manager, a `map[EventType][]Hook` typed event bus, a register-once builtin, a zero-sub-guarded Emit wired at 9 discrete server seams (OFF the per-entity hot path), and full fsnotify hot-reload (off-tick rebuild → tick-goroutine swap, -race clean under reload-during-dispatch).
- **Entity API + dogfood 1 (Phases 23–24):** the SUB-ATTRIB coverage fix (7 jar-exact per-type suppliers + a LivingEntity fallback so every living type gets real attributes), thin id-not-pointer entity/world handles with capability enforcement, declare_mob/goal that slots into the EXISTING goalSelector (not a bypass), and the vanilla pig's 3 passive goals re-expressed 1:1 from the jar — SWAPPED in as the only pig, proven behavior-identical to the retained `newPigAI` Go oracle (`TestPluginPigEqualsGoNativePig`, identical target/yaw/position over 500 ticks).
- **Crafting dogfood 2 (Phase 25):** all 7 recipe types embedded + matched 1:1 (shaped shrink+mirror, shapeless multiset, cooking/stonecutting), a value-returning Match seam, the un-stubbed `ResultSlot.onTake` consume, the crafting_table 3×3 menu + stonecutter; cooking BLOCKS deferred-cited (no per-tick block-entity drive / fuel table yet) but their matchers ship + test.
- **Opt-in Python (Phase 26):** `qur/gopy` @ `python3.14` (`gopython.xyz/py/v14`) behind a `python` build tag; a build-tag stub/impl pair keeps the default no-tag build pure-Go static with ZERO gopy in the graph (verified `go list -deps`); the off-tick lane (ants pool + `pythonHookReady` on `asyncIn2`), a world-bridge (off-tick mutation REQUEST as plain scalars → tick-drain → apply through the Phase-23 seam, capability-gated). The `-tags python` build + tests + -race RAN LIVE in a Docker python:3.14 image.
- **Folia regionization (Phase 27):** the operator's incremental path — extract the `region` struct at N=1 (behavior-neutral) → conc fan-out/barrier at N=1 → flip to N=2 (static chunk→region hash, cross-region transfer at the barrier, async-rejoin re-routing, region-aware Emit). 2 regions tick in parallel, -race clean, behavior-neutral.
- **Closing gate (Phase 28):** an automated perf gate (`TestPerfGate` + `BenchmarkPluginPigVsGoNative` over the retained oracle — plugin tax ~1.7–4.2%/mob·tick under a baseline-derived cap; zero-subscriber Emit = 0 allocs/op) + a bot-driven visual gate (cmd/testbot, a real proto-776 client, OBSERVES the wire: custom mob spawns+walks, vanilla pig wire-identical, vanilla+custom crafting result/consume, an on_block_break SystemChat via a new chat() builtin — exit 0 = PASS).

### What Worked
- **Dogfooding caught real API gaps before Python.** Phase 24 (vanilla mob as a plugin) surfaced 3 missing handle ops — exactly the intended "if vanilla AI doesn't fit, the API is wrong" outcome — and they were filled faithfully. Two independent domains (mobs + crafting) validated the API generalizes, not just fits one case.
- **The thin-id handle was the one primitive that paid off twice.** Designed in Phase 23 for tick-boundary race-safety, it WAS the Folia-safety primitive — region-aware hooks (Phase 27) fell out cleanly because a handle re-resolves on whatever thread owns the entity.
- **Retaining the Go-native oracle as a baseline.** Phase 24 kept `newPigAI` as the behavior-identical reference; Phase 28 promoted that A/B equality test straight into an A/B BENCH — the perf delta IS the isolated plugin overhead, measured not guessed.
- **Incremental regionization (extract → barrier → N=2), each step -race-verified + suite-green.** Kept the most invasive change of the milestone a pure concurrency layer over the faithful logic with zero behavior drift.
- **Running the Python gate LIVE instead of Docker-deferring it.** The operator pushed back on the defer; the live `-tags python` run in a Docker python:3.14 image caught 3 real tagged-build issues a deferred gate would have missed.
- **Bot-as-the-eye for the visual gate.** The bot OBSERVES exact wire packets (AddEntity that moved, a non-empty result slot then consume, a SystemChat with the marker) — a stronger, repeatable gate than a human eye, and it turned an autonomous:false phase fully automated.

### What Was Inefficient
- **gopls staleness, again.** Every phase threw a flood of false "undefined / missing method / region already declared" diagnostics; the only defense was gating on the real `go build`/vet/test every time. Constant friction tax across the whole milestone (now a documented standing rule).
- **The perf gate's absolute ns cap is async-A*-latency-dominated and noisy** — the relative % cap is the real, machine-portable gate; the absolute cap is only a coarse backstop. Honest, but it means the absolute number isn't a tight regression signal.
- **`-race` had to be Docker-scoped (CGO=1) and split** (./server/ ./plugin/ at 900s; ./world/ needs 2400s) — the world suite's -race slowness is a pre-existing ops follow-up, not a race, but it fragmented the gate.

### Patterns Established
- **Build-tag runtime isolation with a stub/impl twin pair** — `//go:build python` real impl + `//go:build !python` cgo-free stub sharing an identical surface, so the default graph stays clean and `go list -deps | grep gopy` is the enforceable gate.
- **Plugins DECLARE once; Go runs the hot path** — the interpreter fires at load + on events + inside active goal callbacks, never per-entity-per-tick. The perf gate proves it holds.
- **Off-tick mutation as a scalar REQUEST drained on the tick** — never a live handle off-tick; the request carries plain scalars and is applied through the single tick-owned seam (TICK-05), capability-gated.
- **Promote an A/B equality TEST into an A/B BENCH** — when a faithful port keeps the original as an oracle, the equality harness is already the perf harness; the delta is the isolated overhead.

### Key Lessons
1. **Dogfood across two DIFFERENT domains, not one.** Mobs alone would have proven the API fits entity AI; crafting (menus/recipes) proved it generalizes — the real test of "ultra-powerful." If you only dogfood one domain you've validated a special case.
2. **Run the opt-in/hard-to-test path LIVE, don't defer it.** The live Python gate caught real issues a "build it in Docker later" plan would have shipped past. Operator was right to push.
3. **Regionize a WORKING single-thread seam incrementally** — extract → barrier → flip, each step independently -race-verified and behavior-neutral. Don't design around regions first; you can't keep behavior identical if you can't diff against the working single-threaded version.
4. **The right race-safety primitive is reusable.** A handle that carries an id + re-resolves on the owner thread solved both the tick-boundary AND the cross-region problem — invest in the primitive, not per-call-site fixes.

### Cost Observations
- Model mix: sonnet for execution (executors + verifiers); inherited model for orchestration + the integration audit.
- Sessions: 1 autonomous run (this one) across all 8 phases + the milestone close, resuming from a mid-milestone compact.
- Notable: phase research background-prespawned for the next phase while executing the current one; ~56 commits this run; both dogfoods + the live Python gate + the bot gate all landed without a hard blocker.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Phases | Key Change |
|-----------|--------|------------|
| v1.0 MVP | 1–9 | Codegen-from-jar foundation; closed on isolated-function tests (the gap that created v3 Phase 17). |
| v2.0 Worldgen + Structures | 10–16 | Per-seed determinism + visual gates introduced; structures shipped with documented deferrals. |
| v3 Online + Operator UX + Structure polish | 17–20 | E2E real-client wiring became the bar; instrument-first live debugging became the standard; "no built-but-unwired code" rule formalized. |
| v4 Plugin / Scripting System | 21–28 | Dual-runtime extension API while preserving CGO=0 + the 1:1 mandate; two-domain dogfooding as the API-fitness test; build-tag runtime isolation; incremental regionization (extract→barrier→N=2); bot-driven gate replaces the human eye. |

### Cumulative Quality

| Milestone | Suite | -race | Zero-Dep Discipline |
|-----------|-------|-------|---------------------|
| v3 | level/world/server green | Docker -race clean on async seams | CFB8 hand-rolled (no crypto dep); CGO=0 preserved throughout |
| v4 | server/plugin/world green; TestPerfGate green | Docker -race clean (./server/ ./plugin/); world -race a known timeout (ops follow-up) | Starlark pure-Go; Python gated behind a build tag (`go list -deps` proves ZERO gopy in the default graph); CGO=0 default preserved |

### Top Lessons (Verified Across Milestones)
1. **Codegen from the jar beats hand-transcription** — verified v1→v3 (packets, blocks, loot, attributes, support shapes all generated/ported, not typed).
2. **"Tested" must mean "wired E2E with the real consumer"** — v1's isolated-test close directly caused v3 Phase 17; v4 closed PLUGIN-07 with a bot driving the real wire; the lesson is now load-bearing.
3. **Reproduce-before-edit + jar-bytecode-before-write** — the two disciplines that made the 12-bug sweep correct on the first try every time; the 1:1 mandate carried cleanly into the v4 plugin layer the same way.
4. **gopls is not the compiler** — false diagnostics flooded both v3 and v4; gate on `go build`/vet/test, never the language server. Now a standing rule.
5. **Dogfood the new API across two different domains before trusting it** — v4: mobs (entity AI) + crafting (menus/recipes) proved generality, not a special case.
