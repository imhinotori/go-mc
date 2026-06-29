# Phase 28: Plugin system visual + perf gate — Context

**Gathered:** 2026-06-28
**Status:** Ready for planning
**Source:** Operator decisions + v4-PLAN.md + 28-RESEARCH.md
**Requirement:** PLUGIN-07

<domain>
## Phase Boundary

**Delivers:** the CLOSING gate of v4 — confirm the whole plugin system works + the perf target holds. This phase is mostly a BENCHMARK + a BOT-DRIVEN verification + the milestone close, NOT new gameplay (the gameplay is built in 21-27). It closes v4.

**autonomous status — CHANGED by operator:** v4-PLAN marked this autonomous:false (human visual gate). The OPERATOR DIRECTED using the test BOT (cmd/testbot, the protocol-776 client used for v3 live verification) to drive the visual gate PROGRAMMATICALLY. So the visual gate is BOT-AUTOMATED — the bot connects as a real 776 client, triggers the actions, and OBSERVES the server's packets (AddEntity/move/container/etc.) to confirm each behavior. NOT a manual human sign-off.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Bot-driven visual gate (operator-directed — replaces the human eye)
- Use `cmd/testbot` (+ `bot/mcbot.go`, ~1100 LOC, a real proto-776 client: login + place already) — EXTEND it to drive + observe the Phase-28 checklist. The bot is a real client, so what it OBSERVES on the wire IS the "visual" confirmation.
- **The checklist the bot verifies (each = trigger via test-kit/action → assert on the observed packets):**
  1. **Custom non-vanilla mob plugin works:** trigger the test-kit spawn of the wander mob → the bot observes an AddEntity (rendered as its wire type) + subsequent move packets (it MOVES via the Go nav).
  2. **Vanilla-mobs-as-plugins behavior-identical:** a plugin pig spawns → the bot observes it acting like a vanilla pig (moves/looks — the same AddEntity + move/headrot the Go-native path produced; the Phase-24 oracle equivalence already proven in tests, the bot confirms it on the wire).
  3. **Crafting through the plugin path:** the bot opens a crafting_table (or the 2×2), places ingredients, observes the result slot populate + the consume (ContainerSetContent/SetSlot) — vanilla recipe + a custom recipe.
  4. **Event system fires:** the bot breaks a block / joins → a plugin hook visibly reacts (observable effect — e.g. a chat line / a follow-on packet the hook produces).
  5. **(Python, Phase 26 built):** a Python plugin runs off-tick (observable via its effect) — verify the -tags python build path is exercised OR note it runs in the python3.14 Docker image (the live gate already ran in Phase 26).
  6. **(Folia, Phase 27 built):** the world ticks in parallel regions — already proven by TestTwoRegionsTickInParallel + -race; the bot confirms gameplay is identical across regions (an entity crossing the region boundary keeps behaving).
- The bot run is scripted + asserts programmatically (exit 0 = gate pass), driven against `run-debug.sh` (SULFUR_TEST_KIT=1). Add the test-kit spawn trigger the gate needs (research: a SULFUR_TEST_KIT spawn-egg / debug command calling spawnDeclaredMob for the CUSTOM wander mob, + boot-load that custom decl into the LIVE registry — Phase 23 left it in an isolated testdata root).

### The PERF gate (measurable, automated)
- A Go benchmark (testing.B) proving "the plugin layer adds NO measurable per-tick cost vs Go-native." The ORACLE ALREADY EXISTS: Phase 24 KEPT newPigAI + the 3 Go goal structs as the behavior-identical baseline, and TestPluginPigEqualsGoNativePig already drives Go-pig vs plugin-pig. Turn that A/B TEST into an A/B BENCH: BenchmarkPluginPigVsGoNative (N mobs × M ticks, plugin path vs Go-native path) — the delta IS the isolated plugin overhead (both share the Go nav/physics).
- Also: the per-tick Emit overhead bench (zero-subscriber guard → unsubscribed events ≈ free) + (if measuring regions) the 2-region throughput vs 1-region.
- **Threshold:** run the bench ONCE for a real baseline, then set BOTH an absolute cap (ns per mob·tick) AND a relative cap (the plugin path within a small % of Go-native — the declare-once design means the interpreter only fires in active goal callbacks, so the delta should be small + bounded). The planner sets the exact numbers from the baseline run (don't guess up front). Honest measurement: isolate the starlark.Call cost from the shared nav/physics.

### Closes v4
- Phase 28 is the LAST phase. After it: the milestone audit + completion (v4-MILESTONE-AUDIT → complete-milestone). The gate IS v4's definition-of-done proof. The bot-pass + the perf-pass + the audit close v4.

### Where the code lives
- The bench: server/*_bench_test.go (the testing.B A/B, matching the existing world/noisegen_bench_test.go style — no new dep). The test-kit spawn trigger: the existing SULFUR_TEST_KIT path + boot-load the custom mob decl. The bot harness: extend cmd/testbot/main.go with the Phase-28 scripted checklist + assertions.
</decisions>

<canonical_refs>
## Canonical References

- `.planning/v4-PLAN.md` — Phase 28 row + gate (the full checklist; closes v4).
- `.planning/REQUIREMENTS.md` — PLUGIN-07 full.
- `.planning/phases/28-plugin-system-visual-perf-gate/28-RESEARCH.md` — HIGH-confidence: the perf oracle already exists (Phase 24 kept newPigAI; turn the A/B test into a bench), the test-kit spawn trigger needed, the conditional 26/27 scope, the bench methodology (testing.B, isolate the starlark.Call delta), the autonomous:false-can't-auto-pass note (now bot-driven per operator).
- `cmd/testbot/main.go` + `bot/mcbot.go` — the proto-776 client bot to extend for the gate.
- `run-debug.sh` (SULFUR_TEST_KIT=1, the debug server the bot connects to), the test-kit hotbar.
- The prior-phase proofs: `TestPluginPigEqualsGoNativePig` (Phase 24, the bench oracle), `TestTwoRegionsTickInParallel` (Phase 27), the Phase-26 -tags python live gate.
- `CLAUDE.md` — CGO=0, -race, push development, no Claude attribution.
</canonical_refs>

<specifics>
## Specific Ideas

- The bot is a real client → its observed packets ARE the visual proof. AddEntity + move = "the mob spawns + moves"; ContainerSetContent on the result slot = "crafting works"; a hook's follow-on packet = "events fire". This is a STRONGER gate than a human eye (it asserts exact wire behavior).
- The perf bench reuses the Phase-24 oracle (newPigAI kept) — the A/B is already wired as a test; promote it to a bench. The delta = the plugin overhead. Run the baseline, set the threshold from it.
- The test-kit spawn trigger: the wander-mob decl from Phase 23 lived in an isolated testdata root (deliberately not boot-loaded live); Phase 28 boot-loads a custom mob decl into the LIVE registry + adds a SULFUR_TEST_KIT spawn trigger (spawn-egg item or a debug command → spawnDeclaredMob).
- 26/27 scope: both LANDED (Python live-gated in Docker, Folia 2-region proven). The bot confirms gameplay-identical; the Python off-tick path is exercised via its Docker gate (already green) — the bot run on the default (CGO=0, no-python) binary confirms the Starlark core; note the Python lane is verified by Phase 26's live gate.
- The bot run = a scripted scenario, exit 0 on all assertions = gate PASS. Run it against run-debug.sh. Capture the transcript.

## Open items the planner resolves
- The exact perf threshold (from the baseline bench run).
- The bot scenario script + the per-item assertions (which packets prove which checklist item).
- The test-kit spawn trigger shape (spawn-egg vs debug command).
- Whether to run the bot gate on the default binary (Starlark core) + note Python via its Docker gate.
</specifics>

<deferred>
## Deferred Ideas

- A continuous perf-regression CI bench — Phase 28 sets the baseline + the gate; wiring it into CI as a regression guard is a follow-up.
- A richer multi-bot scenario — the single-bot scripted gate proves PLUGIN-07; multi-bot stress is future.
</deferred>

---

*Phase: 28-plugin-system-visual-perf-gate*
*Context gathered: 2026-06-28 via operator decisions (bot-driven gate) + v4-PLAN.md + 28-RESEARCH.md*
