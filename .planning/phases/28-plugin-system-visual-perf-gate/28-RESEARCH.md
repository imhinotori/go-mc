# Phase 28: Plugin System Visual + Perf Gate — Research

**Researched:** 2026-06-28
**Domain:** Acceptance gate engineering — a Go `testing.B` perf benchmark suite proving "no measurable per-tick cost vs Go-native", an operator visual test script for a real 26.2 client, and the milestone-closing audit. This is the `autonomous:false` CLOSING gate of v4 (PLUGIN-07).
**Confidence:** HIGH on the Sulfur code map (the real types to benchmark are all read from source this session), the test-kit/run-debug harness, and the Go benchmark methodology; MEDIUM on the exact perf pass threshold (a project decision, flagged Open Question) and on the 26/27-dependent scope (Phase 26 Python is mid-execution, Phase 27 Folia is not started — flagged Open Question).

## Summary

Phase 28 ships almost NO new gameplay code. The gameplay is all built and verified in Phases 21–25 (Starlark runtime, host+event bus, entity/mob API, the 1:1 vanilla pig plugin, crafting-as-plugin). This phase produces three deliverables: (1) a **Go `testing.B` benchmark suite** that proves the plugin layer adds no measurable per-tick cost versus the Go-native path — leaning directly on the oracle Phase 24 deliberately KEPT (`newPigAI` + the three Go goal structs, retained as the behavior-identical comparison baseline); (2) an **operator visual test script** mapping each gate claim to a concrete in-game action on a real 26.2 client, wired through additions to the existing `SULFUR_TEST_KIT` starter kit and the `run-debug.sh` launch; and (3) the **milestone-closing audit** (`v4-MILESTONE-AUDIT.md`, following the `v1.0-MILESTONE-AUDIT.md` template already in `.planning/`) that records the human sign-off and closes v4.

The single most important planning fact: **the benchmark's oracle already exists.** Phase 24 retained `newPigAI` + `randomStrollGoal`/`lookAtPlayerGoal`/`randomLookAroundGoal` precisely so the plugin pig could be proven behavior-identical to the Go-native path (`TestPluginPigEqualsGoNativePig`, 500 ticks, identical observable tuple). Phase 28 turns that same A/B pairing into an A/B *benchmark*: drive N Go-native pigs vs N plugin pigs through `serverAiStep` for M ticks and compare nanoseconds/tick + allocs/op. The honest measurement isolates the `starlark.Call` cost that fires ONLY inside an active goal's `tick`/`canUse`/`start`/`stop` (Phase 23's interpreter-only-at-the-active-seam invariant, `TestNoInterpreterWhenIdle` proves an idle declared mob makes 0 calls/tick) from the shared Go nav/physics both paths run identically. Because the interpreter fires only at declared seams (declare-once architecture), the per-tick delta is small and bounded — the benchmark's job is to MEASURE it and assert it stays under the agreed threshold, not to discover whether it is zero (it is not exactly zero; the claim is "no MEASURABLE per-tick cost", i.e. within a small, stated budget).

**Primary recommendation:** Build three `*_bench_test.go` files in `package server` using the standard `testing.B` + `b.ReportAllocs()` + `b.ResetTimer()` pattern already used by `world/noisegen_bench_test.go`: (1) `BenchmarkPluginPigVsGoNative` (the A/B mob bench, reusing the `TestPluginPigEqualsGoNativePig` harness — same world, same id-seed, `drainPendingPath` to remove async jitter); (2) `BenchmarkTickEmitOverhead` (the per-tick `Emit` cost with 0 subscribers vs 1 subscriber, proving the zero-subscriber guard is free); (3) `BenchmarkServerAiStepPluginVsGo` (the per-`serverAiStep` delta in isolation). Add a small test-kit extension (a spawn-egg-style hotbar item that calls `spawnDeclaredMob` for a CUSTOM wander mob, plus the existing `crafting_table` block + a custom recipe) so the operator can trigger every visual checklist item in-game. Write the visual checklist as an action→expected-result script in the RESEARCH/PLAN, and the milestone audit as the definition-of-done proof that records the human sign-off. Do NOT build a custom profiler (use `go test -bench -benchmem`), do NOT benchmark the shared nav/physics as if it were the plugin delta, and do NOT attempt to auto-pass the `autonomous:false` human gate.

## User Constraints

> No CONTEXT.md exists for Phase 28 yet (`has_context: false` — discuss-phase has not run). The constraints below are extracted verbatim-where-possible from REQUIREMENTS.md (PLUGIN-07), v4-PLAN.md (the Phase 28 row + gate), and CLAUDE.md. Treat them as LOCKED until a discuss-phase produces CONTEXT.md.

### Locked Decisions (from REQUIREMENTS.md PLUGIN-07 + v4-PLAN.md line 87)
- **`autonomous:false` — a HUMAN verifies on a real vanilla 26.2 client.** [CITED: v4-PLAN.md line 87, REQUIREMENTS.md line 42] This gate CANNOT be auto-passed. The deliverable enables the human verification; it does not replace it.
- **The visual gate (real-client confirm), all of these must hold:** [CITED: REQUIREMENTS.md line 42, v4-PLAN.md line 87]
  - a **custom non-vanilla mob plugin** works (declare a wander mob, spawn it, see it move);
  - the **vanilla-mobs-as-plugins** path is **behavior-identical** to Go-native (the plugin pig looks/acts like a vanilla pig);
  - **crafting (vanilla + a custom recipe)** works through the plugin path (open a crafting_table, craft both, see result + consume);
  - the **event system fires** (a plugin hook visibly reacts to a block break / join / damage);
  - **(if Phase 26 lands)** a Python plugin runs off-tick;
  - **(if Phase 27 lands)** the world ticks in parallel regions.
- **The PERF gate:** the plugin layer adds **NO MEASURABLE per-tick cost vs Go-native**, and (if Folia lands) **Folia regions scale**. [CITED: REQUIREMENTS.md line 42, v4-PLAN.md line 87]
- **This phase CLOSES v4** — it is the last phase; after it, v4 is audited + completed. [CITED: v4-PLAN.md line 87 "Closes v4"]
- **`CGO_ENABLED=0` default binary.** [CITED: CLAUDE.md, v4-PLAN.md line 141] The benchmark suite + test-kit additions must build CGO=0; only the (optional) Python bench is build-tag-gated.
- **TICK-05 single-owner / Docker `-race` clean.** [CITED: CLAUDE.md, v4-PLAN.md line 143] The benchmarks run tick-owned state on one goroutine; the `-race` gate carries (though benchmarks themselves are usually not `-race` runs — the existing pig path is already `-race`-verified).
- **1:1 vanilla gameplay mandate (the ONLY permitted deviation is optimization).** [CITED: CLAUDE.md] The CUSTOM gate mob/recipe are free of the mandate by definition (custom = non-vanilla); the vanilla pig + vanilla recipes remain the jar-faithful path proven in 24/25.
- **No Co-Authored-By / no Claude attribution in commits.** [CITED: CLAUDE.md global behaviour rules, v4-PLAN.md line 144]
- **Push target `development`.** [CITED: v4-PLAN.md line 145, REQUIREMENTS.md line 10]

### Claude's Discretion
- The exact perf pass threshold (a small % per-tick overhead vs an absolute µs/tick budget — see Open Questions; RECOMMEND a stated absolute ns/op delta budget AND a relative % cap, both asserted in the bench-as-test).
- The benchmark file layout (one file vs three), the N-mobs / M-ticks bench parameters, and whether the perf assertion lives in a `b.Run` sub-benchmark or a companion `TestPerfGate` that runs the bench logic and asserts the threshold.
- The exact test-kit additions (which hotbar slot, which custom-mob spawn mechanism — a spawn-egg-style item vs a debug command).
- The visual sign-off RECORD format (RECOMMEND a checklist section appended to the milestone audit, each item marked pass/fail by the operator with a note).

### Deferred Ideas (OUT OF SCOPE for Phase 28)
- **Building any Phase 26 (Python) or Phase 27 (Folia) gameplay here.** Phase 28 GATES whatever 21–27 delivered; it does not build the runtimes/regions. If 26/27 have not landed when 28 runs, their gate items are recorded as N/A-not-built, not failures (see Open Questions).
- **A custom profiler / flamegraph tooling.** Use `go test -bench -benchmem` (and optionally `-cpuprofile` for diagnosis only).
- **Re-doing the 1:1 fidelity verification** of the pig/recipes — that is Phase 24/25's verified scope; Phase 28 only re-confirms it observably on a real client.
- **CI perf-regression gating infrastructure** (storing baselines, `benchstat` trend tracking) beyond what the phase needs to assert the one threshold.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PLUGIN-07 | A real vanilla 26.2 client + a perf benchmark confirm: a custom non-vanilla mob plugin works, the vanilla-mobs-as-plugins path is behavior-identical to Go-native, crafting (vanilla + a custom recipe) works through the plugin path, the event system fires correctly, and the plugin layer adds NO measurable per-tick cost vs Go-native (Folia regions scale). VISUAL + PERF GATE, autonomous:false — closes v4. | The whole document. **Architecture Patterns** gives the A/B benchmark design (plugin-path vs the retained `newPigAI` Go oracle), the per-tick `Emit`-overhead bench, and the visual-test-script structure, all named against the real `TickLoop`/`serverAiStep`/`Emit`/`spawnDeclaredMob` types. **Validation Architecture** gives the perf threshold + the visual checklist + the human sign-off record. **Code Examples** give `BenchmarkPluginPigVsGoNative`, the `Emit` overhead bench, and the test-kit additions. **Open Questions** flag the threshold + the 26/27-dependent scope. |

## Architectural Responsibility Map

Phase 28 is a TEST/BENCHMARK/AUDIT phase; the "capabilities" are verification responsibilities, mapped to the tier that owns the proof.

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Per-tick cost measurement (plugin vs Go-native) | **Go test/bench tier** (`package server`, `*_bench_test.go`) | — | The cost lives in `serverAiStep` + `Emit`, both on the tick goroutine; a `testing.B` over those exact functions is the only honest measurement. |
| Behavior-identity re-confirmation | **Go test tier** (the existing `TestPluginPigEqualsGoNativePig`) + **real-client visual** | Operator | Programmatic identity is already proven (Phase 24); the visual gate re-confirms it observably. |
| Custom-mob-works proof | **Real-client visual** (operator) | Test-kit spawn seam (`spawnDeclaredMob`) | A wander mob moving on-screen is the gate; the test kit provides the in-game trigger. |
| Crafting-through-plugin proof | **Real-client visual** (operator) | Test-kit (`crafting_table` block + custom recipe) | Open the table, craft, see result+consume — only a real client confirms the menu/result/consume wire path end-to-end. |
| Event-fires proof | **Real-client visual** (operator) | A gate plugin subscribing to break/join/damage via `Emit` | A visible reaction (chat message / particle / log) to a block break confirms the dispatch seam fires on the real tick. |
| Region-scaling proof (IF 27 lands) | **Go bench tier** (regions-scale bench) + **real-client visual** | — | 2 regions ≈ 2× throughput is a benchmark; "world ticks in parallel" is observable as sustained TPS under load. |
| Python-off-tick proof (IF 26 lands) | **Build-tag-gated test** (`python` tag) + **real-client visual** | — | Default binary stays CGO=0; the Python proof is a separate tagged build. |
| Milestone close (definition-of-done) | **Audit doc tier** (`v4-MILESTONE-AUDIT.md`) | Operator sign-off | The audit records requirements coverage + the human gate result and closes v4. |

## Standard Stack

**No new dependencies.** This phase uses the Go standard library `testing` package and the already-present plugin/AI code. The Python bench (if 26 lands) rides the `python` build tag already established in Phase 26.

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go `testing` (`testing.B`) | stdlib (Go 1.26.1) | The perf benchmark substrate: `go test -bench`, `b.N` auto-scaling, `b.ReportAllocs()`, `b.ResetTimer()`, `b.Run` sub-benchmarks, `b.ReportMetric` for custom per-tick metrics. | [VERIFIED: source] The project ALREADY benchmarks this way — `world/noisegen_bench_test.go` (`BenchmarkGenerateOneChunk`), `level/palette_test.go`, `save/chunk_test.go`, `net/packet/packet_test.go`, `server/internal/bvh/bvh_test.go` all use `testing.B`. The "decides whether a real client can be fed fast enough" framing in `noisegen_bench_test.go` is the same gate logic. No custom tooling. |
| `go test -benchmem` flag | go toolchain | Per-op allocation count (`allocs/op`, `B/op`) — the plugin path's allocation delta is as important as the time delta (a `starlark.Call` allocs a fresh budget-bounded `starlark.Thread` per active goal callback per the Phase 23/22 isolation design). | [VERIFIED: source] `b.ReportAllocs()` is in `BenchmarkGenerateOneChunk`. The Emit/callHook path (`plugin/host/emit.go`) allocates a fresh thread per hook call — measuring `allocs/op` quantifies exactly that. |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `go test -cpuprofile` / `pprof` | go toolchain | Diagnosis ONLY — if the plugin delta exceeds the threshold, profile to find where (the interpreter, the handle re-resolution, the thread alloc). NOT a deliverable. | If a bench fails the threshold and you need to find why. Do not ship a profiler. |
| `benchstat` (`golang.org/x/perf/cmd/benchstat`) | optional, NOT a go.mod dep | Statistical comparison of two benchmark runs (Go-native vs plugin) with confidence intervals — the rigorous way to state "within X%". | OPTIONAL. If the planner wants a statistically-defensible % delta, run both benches and `benchstat` them. Can be invoked via `go run golang.org/x/perf/cmd/benchstat@latest` without a go.mod entry. RECOMMEND mentioning it but not requiring it. |
| `python` build tag (Phase 26) | existing | The Python-off-tick bench, gated so the default CGO=0 build is untouched. | ONLY if Phase 26 has landed when Phase 28 runs. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `testing.B` benchmark | A hand-rolled timing loop with `time.Now()` deltas | `testing.B` auto-scales `b.N` to a stable measurement, handles warm-up, and integrates `-benchmem` + `-cpuprofile`. A hand-rolled loop reinvents all of it badly and is cold-start biased. Use `testing.B`. (See Don't Hand-Roll.) |
| Bench-asserts-threshold via a companion `TestPerfGate` | A pure `Benchmark*` that a human reads | A pure benchmark produces numbers but does not FAIL CI when the delta blows out. A companion `TestPerfGate` (or a `b`-driven assertion) that runs the same A/B logic and `t.Fatalf`s when the delta exceeds the budget makes the perf gate a hard, repeatable check — not a number someone has to eyeball. RECOMMEND both: a `Benchmark*` for the number + a `TestPerfGate` for the hard assertion. |
| Real-client manual visual gate | An automated screenshot/bot client | The requirement is `autonomous:false` BY DESIGN — a human must confirm the gameplay looks right on an unmodified vanilla client. A bot client is out of scope and does not satisfy the human-judgment gate. |

**Installation:** None. (`go test -bench` ships with the toolchain.)

**Version verification:** No package versions to verify — `testing` is stdlib. `benchstat`, if used, is run via `go run golang.org/x/perf/cmd/benchstat@latest` (no pin needed; diagnostic only).

## Architecture Patterns

### System Architecture Diagram — the gate's three proof paths

```
                    ┌─────────────────────────────────────────────────────┐
                    │  PHASE 28 GATE (autonomous:false — closes v4)        │
                    └───────────────┬─────────────────────────────────────┘
                                    │
         ┌──────────────────────────┼───────────────────────────┐
         ▼                          ▼                            ▼
 ┌───────────────────┐   ┌────────────────────────┐   ┌──────────────────────┐
 │ PERF PROOF        │   │ VISUAL PROOF           │   │ MILESTONE AUDIT      │
 │ (go test -bench)  │   │ (operator + real 26.2  │   │ (v4-MILESTONE-       │
 │                   │   │  client via run-debug) │   │  AUDIT.md)           │
 │ A/B over the      │   │                        │   │                      │
 │ RETAINED oracle:  │   │ test-kit triggers:     │   │ - PLUGIN-01..07 +    │
 │                   │   │  - custom wander mob   │   │   REGION-01 coverage │
 │ newPigAI (Go)     │   │    (spawnDeclaredMob)  │   │ - cross-phase seams  │
 │     vs            │   │  - the vanilla pig     │   │   wired              │
 │ vanilla_pig.star  │   │    (already the only   │   │ - build/test/-race   │
 │ (plugin)          │   │    pig — observe it)   │   │   green              │
 │                   │   │  - crafting_table +    │   │ - the HUMAN gate     │
 │ measured at:      │   │    vanilla+custom      │   │   sign-off RECORD    │
 │  - serverAiStep   │   │    recipe              │   │     │                │
 │  - Emit (0 vs 1   │   │  - event plugin reacts │   │     ▼                │
 │    subscriber)    │   │    to a block break    │   │  VERDICT: PASS/FAIL  │
 │                   │   │  - (26) python off-tick│   │  → v4 CLOSED         │
 │ assert: delta <   │   │  - (27) parallel       │   │                      │
 │  threshold        │   │    regions / TPS holds │   │                      │
 └─────────┬─────────┘   └───────────┬────────────┘   └──────────┬───────────┘
           │                         │                            │
           └─────────────────────────┴────────────────────────────┘
                                     │
                          all three feed the audit's
                          definition-of-done verdict
```

### Component Responsibilities

| Component | File (new/existing) | Responsibility |
|-----------|--------------------|----------------|
| `BenchmarkPluginPigVsGoNative` | NEW `server/plugin_perf_bench_test.go` | A/B: N plugin pigs vs N Go-native pigs through `serverAiStep` for `b.N` ticks; reports ns/op + allocs/op per path. |
| `BenchmarkTickEmitOverhead` | NEW (same file) | `Emit(EventTick,…)` with 0 subscribers vs 1 trivial subscriber; proves the zero-subscriber guard is ~free (1 map read, 0 alloc). |
| `BenchmarkServerAiStepPluginVsGo` | NEW (same file) | Isolated per-`serverAiStep` cost for one plugin pig vs one Go pig (the per-mob delta). |
| `TestPerfGate` | NEW (same file) | Runs the A/B timing and `t.Fatalf`s if the plugin delta exceeds the agreed budget — the HARD, CI-repeatable perf assertion. |
| custom wander-mob test-kit trigger | EXTEND `server/test_kit.go` + a spawn seam | A hotbar item (spawn-egg-style) or debug interaction that calls `spawnDeclaredMob` for a CUSTOM (non-pig-behavior) wander mob so the operator can spawn-and-watch it move. |
| crafting test-kit additions | EXTEND `server/test_kit.go` | A `crafting_table` item in the kit (already have `Chest`); ingredient stacks for a vanilla recipe AND a custom recipe registered by a gate plugin. |
| event gate plugin | NEW `plugins/gate_events/main.star` (or testdata) | Subscribes to `on_break`/`on_join`/`on_damage` via the Phase-22 registration API and reacts visibly (chat / log / particle). |
| `v4-MILESTONE-AUDIT.md` | NEW `.planning/v4-MILESTONE-AUDIT.md` | The definition-of-done audit + the human sign-off record (template: `v1.0-MILESTONE-AUDIT.md`). |
| visual test script | in the PLAN (and optionally a `28-VISUAL-CHECKLIST.md`) | The action→expected-result operator script mapping each gate claim to an in-game action. |

### Pattern 1: The A/B benchmark against the RETAINED oracle
**What:** Reuse Phase 24's deliberate oracle retention. `newPigAI` + the three Go goal structs were KEPT (not deleted) as the behavior-identical comparison baseline; `TestPluginPigEqualsGoNativePig` already drives a Go pig (`newPigAI`) and a plugin pig (`spawnVanillaPigWithID`) with the SAME id-seed through `serverAiStep` for 500 ticks and asserts the identical observable tuple. Phase 28 turns that A/B *test* into an A/B *benchmark*.

**When to use:** This is THE perf-gate measurement. The two paths share the identical Go nav/physics (`navigation.tick`, `moveEntity`) — the ONLY difference is that the plugin path routes the goal `canUse`/`start`/`tick`/`stop` decisions through `starlark.Call` (on a fresh budget-bounded thread per call), while the Go path calls the Go goal struct methods directly. So the A/B delta IS the plugin overhead, cleanly isolated.

**Example:**
```go
// Source: server/plugin_pig_test.go (TestPluginPigEqualsGoNativePig harness) + world/noisegen_bench_test.go (bench style)
// NEW: server/plugin_perf_bench_test.go
func BenchmarkPluginPigVsGoNative(b *testing.B) {
    const nMobs = 100
    b.Run("go-native", func(b *testing.B) {
        loop, _ := newPhysicsLoop()              // installs the vanilla_pig registry already
        installVanillaPigRegistry(loop)
        pigs := make([]*Entity, nMobs)
        for i := range pigs {
            pigs[i] = spawnGoNativePig(loop, int32(1000+i), spawnX, spawnY, spawnZ) // newPigAI path (the oracle)
        }
        b.ReportAllocs()
        b.ResetTimer()
        for n := 0; n < b.N; n++ {
            for _, p := range pigs {
                p.ai.serverAiStep(loop, p)
                drainPendingPath(loop, p)        // remove async A* jitter (the test harness's discipline)
            }
        }
    })
    b.Run("plugin", func(b *testing.B) {
        loop, _ := newPhysicsLoop()
        installVanillaPigRegistry(loop)
        pigs := make([]*Entity, nMobs)
        for i := range pigs {
            pigs[i] = loop.spawnVanillaPigWithID(int32(1000+i), spawnX, spawnY, spawnZ) // the plugin pig
        }
        b.ReportAllocs()
        b.ResetTimer()
        for n := 0; n < b.N; n++ {
            for _, p := range pigs {
                p.ai.serverAiStep(loop, p)
                drainPendingPath(loop, p)
            }
        }
    })
}
```
Read the two sub-bench numbers: `go-native` ns/op is the baseline; `plugin` ns/op minus baseline is the per-(mob×tick) plugin overhead. `allocs/op` quantifies the per-active-goal `starlark.Thread` churn.

### Pattern 2: The per-tick Emit-overhead bench (the zero-subscriber guard proof)
**What:** `tick_phases.go:59-61` fires exactly ONE `Emit(EventTick,…)` per tick TOTAL, guarded by the zero-subscriber fast path in `plugin/host/emit.go` (`if len(hooks) == 0 { return }` — one map read, zero alloc, BEFORE `payload.toStarlark()`). The bench proves an unsubscribed server pays nothing and a subscribed server pays exactly one bounded callback.

**When to use:** Proves the "plugins loaded but the hot event unsubscribed is free" half of the no-measurable-cost claim.

**Example:**
```go
// Source: plugin/host/emit.go + plugin/host/emit_test.go
func BenchmarkTickEmitOverhead(b *testing.B) {
    b.Run("zero-subscribers", func(b *testing.B) {
        m := newManagerNoHooks()
        b.ReportAllocs(); b.ResetTimer()
        for n := 0; n < b.N; n++ {
            m.Emit(host.EventTick, host.TickEvent{Tick: n})  // hits the len==0 fast path
        }
    })
    b.Run("one-subscriber", func(b *testing.B) {
        m := newManagerWithTrivialOnTick()                    // one no-op on_tick hook
        b.ReportAllocs(); b.ResetTimer()
        for n := 0; n < b.N; n++ {
            m.Emit(host.EventTick, host.TickEvent{Tick: n})  // toStarlark + fresh thread + starlark.Call
        }
    })
}
```
The `zero-subscribers` sub-bench should report ~0 allocs/op and a few ns/op (a map read). The `one-subscriber` number is the per-tick cost of a SUBSCRIBED on_tick hook — useful to document but NOT on the unsubscribed default path.

### Pattern 3: The visual test script (action → expected on-screen result)
**What:** Map each gate claim to a concrete in-game action the operator performs on a real 26.2 client launched via `run-debug.sh` (which already sets `SULFUR_ONLINE_MODE=1 SULFUR_TEST_KIT=1 SULFUR_PERSIST_CHUNKS=1 SULFUR_ULTRA_DEBUG=1 -seed 777`). Each item is `action` → `expected`.

**Structure (the operator script — belongs in the PLAN + the audit's sign-off section):**

| # | Gate claim | Action (in-game) | Expected on-screen result | Pass? |
|---|-----------|------------------|---------------------------|-------|
| 1 | Custom mob plugin works | Use the custom-mob spawn item (test-kit hotbar) at your feet | A non-vanilla wander mob appears and ambles around via the Go nav (visibly moves > a few blocks over ~10s) | ☐ |
| 2 | Vanilla-as-plugin behavior-identical | Find/observe a naturally-spawned pig (the only pig is the plugin pig) | The pig strolls, looks at you when near, and randomly looks around — indistinguishable from vanilla pig AI | ☐ |
| 3 | Crafting — vanilla recipe | Place the `crafting_table` (test-kit), right-click to open the 3×3, lay out a known vanilla recipe (e.g. planks→sticks), take the result | The result appears in the result slot; taking it consumes the inputs; the crafted item lands in inventory | ☐ |
| 4 | Crafting — custom recipe | Lay out the gate plugin's CUSTOM recipe in the same table, take the result | The custom result appears + inputs consume — proving the recipe came through the plugin path | ☐ |
| 5 | Event system fires | Break any block | The gate event plugin reacts visibly (a chat message / log line / particle) confirming `on_break` fired on the real tick | ☐ |
| 6 | (IF Phase 26) Python off-tick | Trigger the Python gate plugin's heavy action | It runs off-tick (the world keeps ticking smoothly) and rejoins via the async seam | ☐ / N/A |
| 7 | (IF Phase 27) Parallel regions | Move across a region boundary under mob load; watch TPS/MSPT | TPS holds at 20 with the world ticking in parallel regions (no single-thread stall) | ☐ / N/A |

### Pattern 4: The milestone-closing audit
**What:** Follow `.planning/v1.0-MILESTONE-AUDIT.md` exactly: (1) a requirements-coverage table (PLUGIN-01..07 + REGION-01, marked done/N-A), (2) a cross-phase integration section (evidence-based read of the live source — every plugin seam wired end-to-end), (3) build/test/-race results, (4) a gaps section, and (5) the HUMAN gate sign-off record (the visual checklist results + the operator's note + a verdict PASS/FAIL). The audit is the definition-of-done proof that closes v4.

**When to use:** AFTER the perf gate passes AND the operator signs off the visual checklist. The audit records both.

### Anti-Patterns to Avoid
- **Benchmarking the shared nav/physics as the plugin delta.** `serverAiStep` runs the IDENTICAL `navigation.tick`/`moveEntity` in both paths. If you benchmark a bare `serverAiStep` of a plugin pig and report the whole number as "plugin cost", you are reporting mostly Go nav cost. The plugin cost is the A/B DELTA (plugin minus go-native), not the absolute plugin number. (See Pitfall 1.)
- **Cold-interpreter bias.** The first `starlark.Call` after load may pay one-time costs (module already compiled at load, but thread/frame setup amortizes). Use `b.ResetTimer()` after spawning + warm up a few ticks so you measure the steady-state per-tick cost, not first-call setup. (See Pitfall 2.)
- **Auto-passing the human gate.** Writing a test that "confirms" the visual gate defeats `autonomous:false`. The deliverable ENABLES the human check; the verifier/audit must mark the visual items as requiring (and recording) human sign-off.
- **Deleting the Go oracle.** `newPigAI` + the Go goals are the A/B baseline. Do NOT remove them in this phase (a future phase may, once the plugin pig is the established baseline). They are the perf benchmark's "B" side.
- **Treating "no measurable cost" as "exactly zero".** The interpreter fires at active seams; the cost is small but nonzero. The claim is "no MEASURABLE cost" = within the agreed budget. State the budget; assert it; do not claim zero.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-tick timing measurement | A `time.Now()` delta loop with manual averaging | `testing.B` (`b.N`, `b.ResetTimer`, `b.ReportAllocs`) | `testing.B` auto-scales iterations to a stable measurement, handles warm-up, and integrates `-benchmem`/`-cpuprofile`. A hand loop is cold-biased, doesn't converge, and reinvents the harness. The project already uses `testing.B` (`noisegen_bench_test.go`). |
| Allocation counting | A custom `runtime.MemStats` diff | `b.ReportAllocs()` / `-benchmem` | `b.ReportAllocs()` gives `allocs/op` + `B/op` per benchmark op correctly amortized. The plugin path's `starlark.Thread`-per-callback alloc is exactly what this surfaces. |
| Statistical A/B comparison | A hand-rolled t-test on two number sets | `benchstat` (run via `go run golang.org/x/perf/cmd/benchstat@latest`) | If you need a defensible "within X% (p<0.05)" claim, `benchstat` does it properly. Diagnostic/optional, not a dep. |
| CPU hotspot diagnosis (if the bench fails) | `println` timing around suspected lines | `go test -bench … -cpuprofile cpu.out` + `go tool pprof` | The toolchain's profiler attributes cost to the actual functions (the interpreter vs the handle re-resolution vs the thread alloc). |
| The behavior-identity proof | A new comparison harness | The EXISTING `TestPluginPigEqualsGoNativePig` + `drainPendingPath` | It already exists, is verified, and removes async jitter. The benchmark reuses its harness. |
| The visual gate "automation" | A bot/headless client | A human on a real 26.2 client via `run-debug.sh` | `autonomous:false` is the requirement — the human judgment IS the gate. |

**Key insight:** This phase's entire value is MEASUREMENT and HUMAN CONFIRMATION of already-built, already-verified code. Every "build it yourself" temptation here (a profiler, a timing loop, a bot client) is reinventing a standard tool or violating the `autonomous:false` mandate. The only NEW code is the benchmark functions, the small test-kit triggers, the event gate plugin, and the audit doc.

## Runtime State Inventory

> Phase 28 is a test/benchmark/audit phase — it adds bench files, small test-kit triggers, a gate plugin, and a doc. It does NOT rename, refactor, migrate, or alter persisted/registered runtime state. This section is included for completeness with each category explicitly answered.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — the gate spawns mobs/crafts items transiently; no new persisted schema, no datastore keys renamed. `SULFUR_PERSIST_CHUNKS=1` (run-debug) persists chunks but the gate introduces no new persisted state. | None |
| Live service config | None — no external services. The only "config" is env vars in `run-debug.sh` (already set). | None — possibly ADD one env toggle for the custom-mob test-kit trigger (e.g. extend `SULFUR_TEST_KIT`), a code-level addition not a service config. |
| OS-registered state | None — no OS task/process registration. `run-debug.sh` builds + runs a foreground/background process; no scheduler/launchd/systemd entries. | None |
| Secrets/env vars | None renamed. The phase may ADD a test-kit toggle env var (read in `test_kit.go` via `os.Getenv`), purely additive. | None (additive only) |
| Build artifacts | The bench files compile into the `server` test binary (CGO=0). If Phase 26's Python bench is included, it builds under the `python` tag (CGO=1) — a SEPARATE tagged build, never the default. `run-debug.sh` rebuilds `sulfur.exe` (CGO=0) each launch. | Ensure the default `go build`/`go test` stays CGO=0; gate the Python bench behind `//go:build python`. |

**Verified:** the gate adds bench/test files + a doc + small additive test-kit code; no runtime state is renamed, migrated, or re-registered.

## Common Pitfalls

### Pitfall 1: Benchmarking the shared nav/physics instead of the plugin delta
**What goes wrong:** You write `BenchmarkServerAiStepPlugin` over a plugin pig, get (say) 1200 ns/op, and report "the plugin layer costs 1200 ns/tick" — but ~1100 ns of that is the shared `navigation.tick`/`moveEntity`/A*-step that the Go-native pig runs IDENTICALLY. The real plugin overhead is the DELTA (plugin minus go-native), maybe ~100 ns.
**Why it happens:** `serverAiStep` does goal arbitration (the only plugin-touching part) AND nav/physics (shared). Benchmarking the whole function conflates them.
**How to avoid:** ALWAYS run the A/B pair (`b.Run("go-native")` + `b.Run("plugin")`) over the SAME world/seed/mob-count and report the DELTA. The go-native sub-bench is the subtraction baseline. State the threshold against the delta, not the absolute.
**Warning signs:** A "plugin cost" number that barely changes when you add/remove plugin goals (because it's dominated by nav); a number that looks alarmingly large (because it includes the whole AI step).

### Pitfall 2: Warm-vs-cold interpreter / first-call bias
**What goes wrong:** The first few `serverAiStep` calls after spawn pay one-time costs (the mob's first path request, the first goal `canUse` roll, frame setup). If `b.ResetTimer()` is before the spawn+warm-up, or if `b.N` is tiny, the cold cost pollutes the average.
**Why it happens:** `testing.B` runs `b.N` iterations; with a small `b.N` (heavy per-op work) the first cold ops weigh more. The Starlark module is compiled ONCE at load (Phase 21), so per-call compilation is NOT a factor — but per-call thread/frame setup and the first path request are.
**How to avoid:** Spawn the mobs, run a handful of warm-up ticks, THEN `b.ResetTimer()`. Let `b.N` auto-scale. Run `-count=5` and check the variance is small. The existing `TestPluginPigEqualsGoNativePig` already block-drains paths (`drainPendingPath`) to remove async jitter — reuse that.
**Warning signs:** High run-to-run variance; a number that drops sharply when you increase `b.N`.

### Pitfall 3: The async A* jitter contaminating the comparison
**What goes wrong:** `serverAiStep` calls `navigation.requestPath` which submits to the off-tick A* pool (`pathPool`); the result rejoins at a non-deterministic later tick via `applyAsyncResults`. In a tight bench loop the two paths (Go vs plugin) rejoin at different iterations purely from scheduler timing, making the comparison noisy and the mobs diverge in position (different nav work → different cost).
**Why it happens:** The async pathfinding (OPT-01, Phase 8) is inherently non-deterministic in timing.
**How to avoid:** Reuse the `TestPluginPigEqualsGoNativePig` discipline: `drainPendingPath` block-drains each mob's pending path to completion the SAME tick it is requested, so both paths do the identical nav work deterministically. The bench measures the goal/RNG/interpreter LOGIC, not scheduler jitter. (Documented in 24-02-SUMMARY "Issues Encountered".)
**Warning signs:** The two sub-benches' positions diverge; the delta swings wildly between runs.

### Pitfall 4: The `autonomous:false` gate cannot be auto-passed
**What goes wrong:** The verifier (or an over-eager plan) treats the visual checklist as programmatically satisfiable and marks the phase passed without a human ever connecting a client. v4 closes on an unverified gate.
**Why it happens:** Every OTHER v4 phase (21–27) was autonomous/standalone-testable; the habit is to prove everything in code. Phase 28 is the ONE phase where a human must look at the screen.
**How to avoid:** The plan and audit must EXPLICITLY mark the visual items as "requires human sign-off" and provide the record format (the checklist table with pass/fail + operator note). The perf gate IS automatable (the bench + `TestPerfGate`); the visual gate is NOT. The milestone audit's verdict is BLOCKED on the recorded human sign-off. The phase's verifier should report "Human Verification Required: YES" for the visual items (contrast Phase 24's verifier which reported "None").
**Warning signs:** A phase-28 verification that says "Human Verification Required: None"; a milestone audit with no operator sign-off record.

### Pitfall 5: Scoping the gate to features that did not land (26/27)
**What goes wrong:** The plan asserts the Python-off-tick and parallel-regions gate items as hard requirements, but Phase 26 (Python) is mid-execution and Phase 27 (Folia) is not started — so those features may not exist when Phase 28 runs. The gate then "fails" on un-built features that were never in this run's scope.
**Why it happens:** The requirement text (PLUGIN-07) lists all v4 features, written before the 26/27 sequencing was known. STATE.md shows 5/8 phases complete, Phase 26 just starting.
**How to avoid:** The gate scope is CONDITIONAL: "(if Phase 26 lands)" and "(if Phase 27 lands)" are literal in the requirement. The plan must check `.planning/STATE.md` / the phase dirs at plan time and mark un-built features N/A-not-built in the checklist, NOT failures. The Starlark core gate (custom mob, vanilla pig, crafting, events, perf) is ALWAYS in scope; Python + regions are conditional. (See Open Questions.)
**Warning signs:** A checklist item for Python/regions with no "/ N/A" branch; a plan that assumes 26/27 are done without verifying.

### Pitfall 6: Forgetting the test-kit / spawn seam for the custom mob
**What goes wrong:** The operator has no in-game way to spawn the CUSTOM (non-pig) wander mob — `spawnDeclaredMob` is a Go/test seam (Phase 23 explicitly deferred a plugin-facing spawn builtin under T-23-11 `accept`), and the natural spawner only spawns the vanilla pig. So gate item #1 (custom mob works) can't be triggered on a real client.
**Why it happens:** Phase 23 intentionally did NOT add a plugin-facing spawn builtin; `spawnDeclaredMob` is the test/debug seam.
**How to avoid:** Add a test-kit trigger: a spawn-egg-style hotbar item whose use handler calls `t.spawnDeclaredMob(customWanderDecl, …)` near the player (gated by `SULFUR_TEST_KIT`, like the rest of the kit), OR a debug command. Wire it through the existing `handleUseItem`/`useBlockInteraction` path (`server/item_use.go`, `server/block_interact.go`). Register the custom wander-mob declaration at boot alongside (or like) the vanilla_pig embed. This is the ONE bit of new wiring the gate needs.
**Warning signs:** A visual checklist item #1 with no corresponding test-kit trigger in the plan; relying on the natural spawner (which only spawns the vanilla pig) to produce the custom mob.

## Code Examples

### The A/B perf gate as a hard test (the CI-repeatable assertion)
```go
// Source: derived from server/plugin_pig_test.go (TestPluginPigEqualsGoNativePig) + the bench above
// NEW: server/plugin_perf_bench_test.go
//
// TestPerfGate runs the SAME A/B logic as BenchmarkPluginPigVsGoNative but asserts the per-tick
// plugin delta stays within the agreed budget — so the perf gate is a hard, repeatable check, not a
// number a human eyeballs. The threshold is a project decision (see Open Questions); the structure is:
func TestPerfGate(t *testing.T) {
    const nMobs, warmup, ticks = 100, 50, 2000
    goNs := timeAvgTickNs(t, spawnGoNativePigs(nMobs), warmup, ticks)     // baseline (newPigAI oracle)
    plNs := timeAvgTickNs(t, spawnPluginPigs(nMobs), warmup, ticks)       // the plugin path
    deltaNs := plNs - goNs
    deltaPct := 100 * deltaNs / goNs

    // The budget (DISCRETION — see Open Questions): an ABSOLUTE ns/(mob·tick) cap AND a relative %.
    const maxDeltaNsPerMobTick = /* e.g. */ 250.0   // ns of interpreter overhead per active-goal mob-tick
    const maxDeltaPct          = /* e.g. */ 10.0    // and within 10% of the Go-native baseline
    if deltaNs/nMobs > maxDeltaNsPerMobTick {
        t.Fatalf("plugin per-mob-tick overhead %.0f ns exceeds budget %.0f ns", deltaNs/nMobs, maxDeltaNsPerMobTick)
    }
    if deltaPct > maxDeltaPct {
        t.Fatalf("plugin per-tick overhead %.1f%% exceeds budget %.1f%%", deltaPct, maxDeltaPct)
    }
    t.Logf("PERF GATE: go-native %.0f ns/tick, plugin %.0f ns/tick, delta %.0f ns (%.1f%%) over %d mobs",
        goNs, plNs, deltaNs, deltaPct, nMobs)
}
```

### The test-kit custom-mob spawn trigger
```go
// Source: server/test_kit.go (the SULFUR_TEST_KIT pattern) + server/item_use.go (handleUseItem seam)
// EXTEND server/test_kit.go: add a spawn-egg-style entry; on use, spawn the CUSTOM wander mob.
//
// The custom wander-mob declaration is boot-loaded like vanilla_pig (a separate embedded plugin or a
// gate-only testdata decl). Its use handler (gated by SULFUR_TEST_KIT) calls spawnDeclaredMob near the
// player — the SAME Phase-23 path the wander gate used (testdata/mobplugins/wandermob), now operator-
// triggerable in-game.
var testKitSpawnEgg = testKitStack{slot: 35, id: item.PigSpawnEgg.ID, count: 16} // re-skinned as the custom-mob egg in the gate build

func (t *TickLoop) handleGateSpawnEgg(p *tickPlayer) {
    if !testKitEnabled() || t.mobRegistry == nil {
        return
    }
    decl, ok := t.mobRegistry.byName[customWanderMobName] // the gate's CUSTOM (non-vanilla) wander decl
    if !ok {
        return
    }
    x, y, z := p.x, p.y, p.z
    t.spawnDeclaredMob(decl, x, y, z) // Phase-23 seam: NewEntity + seedAttributes + buildAIFromDecl + entities.add
}
```

### The event gate plugin (visible reaction proof)
```python
# Source: plugin/host/event.go (EventType vocabulary) + the Phase-22 registration API
# NEW: plugins/gate_events/main.star — subscribes to the discrete gameplay events and reacts visibly.
def on_break(ev):
    # ev carries the broken block + player; react visibly so the operator SEES the hook fired.
    log("gate_events: on_break fired at %d,%d,%d" % (ev.x, ev.y, ev.z))   # log line proof
    # (if a chat/particle host seam is exposed: send a chat line to the breaker — the on-screen proof)

def on_join(ev):
    log("gate_events: on_join fired for %s" % ev.name)

def on_damage(ev):
    log("gate_events: on_damage fired (%.1f)" % ev.amount)

# register-once at load (the Phase-22 API): the host wires these to EventBreak/EventJoin/EventDamage.
subscribe("on_break", on_break)
subscribe("on_join",  on_join)
subscribe("on_damage", on_damage)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| "Prove everything in code" (Phases 21–27 all autonomous) | A HUMAN visual gate on a real client (Phase 28) | This phase | The verifier must report "Human Verification Required: YES"; the audit blocks on the operator sign-off. |
| Per-tick interpreter call per entity (the rejected design) | Declare-once + Go-runs-the-hot-path; interpreter fires only at active goal seams | Phase 23 architecture | The perf delta is small + bounded BY DESIGN — the bench MEASURES this; it does not have to defend against a per-entity-per-tick interpreter storm (that was architected out, `TestNoInterpreterWhenIdle`). |
| `newPigAI` as the live spawn path | `spawnVanillaPig` (plugin) is the only pig; `newPigAI` RETAINED as the oracle | Phase 24 | The perf benchmark's "B" baseline already exists — do not delete it. |

**Deprecated/outdated:**
- None relevant. `testing.B` is the stable, current Go benchmark API (no `b.Loop` migration needed for this — the classic `for n := 0; n < b.N; n++` form is what the project uses and is correct; Go 1.24+ added `b.Loop()` as an alternative but it is optional and the project's existing benches use the classic form — match the existing style).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The perf threshold is a small absolute ns/(mob·tick) budget AND a relative % cap (example values 250 ns / 10%) | Code Examples, Validation | The example numbers are ILLUSTRATIVE. The real budget is a project decision (Open Q1). Risk: if set too tight, a passing system fails the gate; too loose, a regression passes. Must be set by the user/operator from an actual baseline run. |
| A2 | Phase 26 (Python) and Phase 27 (Folia) may NOT be built when Phase 28 runs | User Constraints, Pitfall 5, Open Q2 | STATE.md shows 5/8 complete, Phase 26 mid-execution, Phase 27 not started. If both land before 28, their gate items become hard (not N/A). The plan MUST re-check at plan time. Risk: gating un-built features as failures, or skipping built features. |
| A3 | A test-kit spawn trigger (spawn-egg-style item or debug command) is the right mechanism for the custom mob, since `spawnDeclaredMob` is a Go/test seam and no plugin-facing spawn builtin exists | Architecture, Pitfall 6 | Phase 23 deferred the plugin-facing spawn builtin (T-23-11 accept). If a later phase added one, the mechanism could be plugin-driven instead. Verify the spawn seam state at plan time. |
| A4 | The custom wander mob declaration can be boot-loaded like vanilla_pig (embedded or gate testdata) | Architecture, Code Examples | The wander gate (Phase 23) used `testdata/mobplugins/wandermob` in an isolated root; a real-client gate needs it loaded into the LIVE registry. Risk: load-path wiring (the isolated testdata root was deliberately NOT loaded live). May need a small embed like vanilla_pig. |
| A5 | The event host exposes (or can cheaply expose) a VISIBLE reaction seam (chat/log/particle) for the gate plugin | Code Examples, visual checklist #5 | `log()` is the safe minimum (always available). A chat-to-player seam may need a tiny host addition if not already exposed. Risk: the "visible" proof is log-only, which is less of an on-screen confirmation — confirm what plugin-facing output seams exist at plan time. |
| A6 | `v4-MILESTONE-AUDIT.md` follows the `v1.0-MILESTONE-AUDIT.md` structure and is the close mechanism | Architecture Pattern 4, Validation | Verified the v1.0 template exists and its structure. The exact close workflow (does GSD `/gsd-audit-milestone` generate it, or is it hand-written?) is a process detail — the CONTENT is what matters here. |

## Open Questions

1. **What is the exact perf pass threshold?**
   - What we know: the claim is "no MEASURABLE per-tick cost vs Go-native"; the interpreter fires only at active goal seams (small, bounded delta by design); the A/B delta is the right quantity to measure (Pitfall 1).
   - What's unclear: the numeric budget. "No measurable" needs a definition — an absolute ns/(mob·tick) cap, a relative % cap, or both. The right values come from an ACTUAL baseline run on the operator's hardware, not a guess.
   - Recommendation: At plan/discuss time, run `BenchmarkPluginPigVsGoNative` once to get a real baseline delta, then set the budget at (baseline delta × a safety margin, e.g. 1.5–2×) AND a relative cap (e.g. ≤10% of the go-native ns/tick). Assert BOTH in `TestPerfGate`. Document the measured baseline in the audit. This is a Claude's-discretion + operator-confirmation item.

2. **Which v4 features are in-scope for THIS run of the gate (does 26/27 land)?**
   - What we know: STATE.md shows Phase 26 (Python) mid-execution, Phase 27 (Folia) NOT started, 5/8 phases complete. The requirement text lists Python + regions as "(if Phase 26/27 lands)" — literally conditional.
   - What's unclear: the actual state of 26/27 when Phase 28 executes (they may both land first, since 28 is sequenced last).
   - Recommendation: the plan MUST re-read `.planning/STATE.md` + check the `26-*`/`27-*` phase dirs for SUMMARY/VERIFICATION at plan time. The ALWAYS-in-scope core gate = custom mob + vanilla pig + crafting + events + the Starlark perf delta. Python + regions are CONDITIONAL: if built, they are hard gate items (Python off-tick visible; regions-scale bench ≈ 2× + parallel-tick observable); if not built, mark N/A-not-built (NOT failures). The regions-scale benchmark (if 27 landed) is a separate `BenchmarkRegionsScale` asserting 2 regions ≈ 2× throughput — design it only if 27 is present.

3. **What is the operator's visual sign-off RECORD format?**
   - What we know: `autonomous:false` requires a human record; the v1.0 milestone audit notes "the Phase-9 real-client visual gate was approved" but the v1.0 audit doesn't show a detailed checklist record.
   - What's unclear: the exact format the operator fills in (a checklist table in the audit, a separate sign-off file, a STATE.md note).
   - Recommendation: a checklist table (the Pattern 3 table) appended to `v4-MILESTONE-AUDIT.md` under a "Human Verification — Visual Gate" heading, each item marked pass/fail with a one-line operator note, plus an overall operator verdict + date. The milestone verdict is BLOCKED until this is filled. RECOMMEND this format; confirm with the operator at discuss time.

4. **Does a plugin-facing spawn builtin exist now, or is the test-kit seam the only option?**
   - What we know: Phase 23 deferred the plugin-facing spawn builtin (T-23-11, disposition `accept`); `spawnDeclaredMob` is the test/debug seam.
   - What's unclear: whether any phase 24–27 added a spawn builtin (none of the read summaries did).
   - Recommendation: the test-kit spawn trigger (Pattern 6 / the code example) is the safe mechanism. Grep for a plugin-facing spawn builtin at plan time; if absent (likely), use the test-kit seam.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain (`go test -bench`) | The perf benchmark suite | ✓ | 1.26.1 | — |
| A real vanilla Minecraft 26.2 client | The visual gate (operator) | ✓ (operator-owned) | 26.2 (protocol 776) | NONE — this is the `autonomous:false` gate; no fallback by design |
| `run-debug.sh` debug launch | The real-client test harness | ✓ | — (sets online-mode + test-kit + persist + ultra-debug, seed 777) | — |
| Docker `golang:1.26` (`-race`) | The race re-confirm (already green in 21–25) | ✓ (used throughout v4) | golang:1.26 | host CGO=0 build (no gcc) — Docker is the standard `-race` path |
| Java 25 + `temp/cache/26.2-inner.jar` | NOT needed (no new 1:1 port; the pig/recipes are already verified) | ✓ (build-time only) | 25 | — (not exercised this phase) |
| CPython / libpython (`python` tag) | ONLY the Python-off-tick bench, IF Phase 26 landed | ⚠ conditional | 3.14 (gopython.xyz/py/v14) | If 26 not built: the Python gate item is N/A; the default CGO=0 bench needs no CPython |
| `benchstat` | OPTIONAL statistical A/B | run-on-demand via `go run …@latest` | — | eyeball the two bench numbers |

**Missing dependencies with no fallback:**
- The real 26.2 client + a human operator — BY DESIGN (`autonomous:false`). Not a blocker; it IS the gate.

**Missing dependencies with fallback:**
- CPython/libpython — only if Phase 26's Python bench is in scope; otherwise N/A and the default CGO=0 path is unaffected.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` (`testing.B` for benchmarks, `testing.T` for the perf-gate assertion + existing identity test) |
| Config file | none — Go convention (`*_bench_test.go` / `*_test.go` in `package server`) |
| Quick run command | `CGO_ENABLED=0 go test ./server/ -run TestPerfGate -count=1` (the hard perf assertion) + `... -run TestPluginPigEqualsGoNativePig` (identity re-confirm) |
| Full suite command | `CGO_ENABLED=0 go test ./server/ ./plugin/... ./level/recipe/ ./level/attribute/` then `go test -bench 'BenchmarkPluginPig|BenchmarkTickEmit|BenchmarkServerAiStep' -benchmem ./server/` then Docker `-race -timeout 900s ./server/ ./plugin/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PLUGIN-07 (perf) | Plugin per-tick overhead within budget vs Go-native | bench + hard test | `go test -bench BenchmarkPluginPigVsGoNative -benchmem ./server/` + `go test -run TestPerfGate ./server/` | ❌ Wave 0 (`server/plugin_perf_bench_test.go`) |
| PLUGIN-07 (perf) | Zero-subscriber Emit is free; subscribed is one bounded call | bench | `go test -bench BenchmarkTickEmitOverhead -benchmem ./server/` | ❌ Wave 0 (same file) |
| PLUGIN-07 (perf) | Per-serverAiStep plugin delta isolated | bench | `go test -bench BenchmarkServerAiStepPluginVsGo -benchmem ./server/` | ❌ Wave 0 (same file) |
| PLUGIN-07 (identity) | Plugin pig behavior-identical to Go-native (re-confirm) | unit | `go test -run TestPluginPigEqualsGoNativePig -count=3 ./server/` | ✅ (`server/plugin_pig_test.go`) |
| PLUGIN-07 (custom mob) | Custom wander mob spawns + moves on a real client | manual-visual | operator via `run-debug.sh` + test-kit spawn item | ❌ Wave 0 (test-kit trigger + custom decl) |
| PLUGIN-07 (crafting) | Vanilla + custom recipe craft through the plugin path | manual-visual + unit | operator (visual) + existing `TestStonecutter*`/crafting tests (auto) | ✅ unit / ❌ test-kit recipe wiring |
| PLUGIN-07 (events) | A plugin hook visibly reacts to break/join/damage | manual-visual | operator + the gate_events plugin | ❌ Wave 0 (`plugins/gate_events/main.star`) |
| PLUGIN-07 (Python, if 26) | Python plugin runs off-tick | manual-visual + tagged test | operator + `go test -tags python ...` | ⚠ conditional on Phase 26 |
| REGION-01 (regions, if 27) | 2 regions ≈ 2× throughput; parallel tick observable | bench + manual-visual | `go test -bench BenchmarkRegionsScale ./server/` + operator TPS | ⚠ conditional on Phase 27 |

### Sampling Rate
- **Per task commit:** `CGO_ENABLED=0 go test ./server/ -run 'TestPerfGate|TestPluginPigEqualsGoNativePig' -count=1` (the perf gate + identity are fast).
- **Per wave merge:** the full quick suite + `go test -bench … -benchmem ./server/` (record the numbers).
- **Phase gate:** full suite green + Docker `-race` green + the perf budget asserted (`TestPerfGate` passes) + the operator visual sign-off recorded in the audit, BEFORE `/gsd-verify-work` and the milestone close.

### Wave 0 Gaps
- [ ] `server/plugin_perf_bench_test.go` — `BenchmarkPluginPigVsGoNative`, `BenchmarkTickEmitOverhead`, `BenchmarkServerAiStepPluginVsGo`, `TestPerfGate` (covers PLUGIN-07 perf). Needs the `spawnGoNativePigs`/`spawnPluginPigs`/`timeAvgTickNs` helpers (reuse the `TestPluginPigEqualsGoNativePig` + `drainPendingPath` + `newPhysicsLoop`/`installVanillaPigRegistry` harness).
- [ ] Test-kit custom-mob spawn trigger — a spawn item + use handler calling `spawnDeclaredMob` + the custom wander-mob declaration boot-loaded into the LIVE registry (covers the custom-mob visual gate). Extends `server/test_kit.go` + `server/item_use.go` + a small embed like `server/vanilla_pig_embed.go`.
- [ ] `plugins/gate_events/main.star` + boot-load — the event gate plugin (covers the events visual gate).
- [ ] Test-kit crafting additions — ensure a `crafting_table` item + ingredient stacks for a vanilla recipe AND a custom recipe (the custom recipe registered by a gate plugin) are in the kit (covers the crafting visual gate).
- [ ] `.planning/v4-MILESTONE-AUDIT.md` — the milestone-close audit + the human sign-off record (template: `v1.0-MILESTONE-AUDIT.md`).
- [ ] (CONDITIONAL on Phase 27) `BenchmarkRegionsScale` — only if Folia landed.
- [ ] (CONDITIONAL on Phase 26) `//go:build python` Python-off-tick bench — only if Python landed.

## Security Domain

> `security_enforcement` is not set to `false` in config — included. Phase 28 is a test/benchmark/audit phase with no new attack surface on the production path. The security-relevant facts are inherited from the gated subsystems (already verified) and noted here for the audit's completeness.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | The gate uses `SULFUR_ONLINE_MODE=1` (run-debug) — real Mojang auth — but adds no new auth code. |
| V3 Session Management | no | No new session handling. |
| V4 Access Control | yes (inherited) | The plugin CAPABILITY model (Phase 23: `entities.read/write`, `world.read/write`, `nav`) gates handle ops; the gate plugins (custom mob, events, custom recipe) must declare least-privilege caps in their manifest — re-confirm at the audit. The vanilla_pig manifest's least-privilege caps (NO `world.write`) are the template. |
| V5 Input Validation | yes (inherited) | The Starlark sandbox (step budget, recursion off, no I/O builtins) bounds a malicious/runaway plugin; the per-callback fresh budget-bounded thread + recover (`plugin/host/emit.go`, `plugin_mob_ai.go`) isolates a bad hook. The gate's test-kit spawn input is operator-triggered, not network-untrusted. |
| V6 Cryptography | no | No new crypto. |

### Known Threat Patterns for the Go plugin gate
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Test-kit spawn item abused to flood the world with custom mobs | Denial of Service | The test-kit is `SULFUR_TEST_KIT`-gated (operator opt-in, off in prod); the spawn is operator-triggered. The natural-spawn CREATURE cap (Phase 23 T-23-11) bounds ambient spawns; the test-kit trigger is a debug seam, not a network-exposed path. |
| A gate plugin with over-broad capabilities | Elevation of Privilege | Each gate plugin (custom mob, events, recipe) declares least-privilege caps in its manifest (Phase 23 enforcement); the audit re-confirms NO gate plugin requests more than it needs (the vanilla_pig template: read/write entities, read world, nav — no world.write unless the recipe path needs it). |
| Runaway/erroring gate hook freezes the tick | Denial of Service | Already mitigated (Phase 22/23): every hook/callback runs on a fresh step-budget thread inside a recover; a runaway returns `*EvalError`, a panic is logged + absorbed, the tick continues. The perf bench should NOT use a pathological hook (it measures the legitimate path). |

## Sources

### Primary (HIGH confidence — read from the live repo this session)
- `D:\ender\.planning\v4-PLAN.md` (Phase 28 row line 87, the gate definition, the constraints lines 140–145) — the plan of record.
- `D:\ender\.planning\REQUIREMENTS.md` (PLUGIN-07 line 42, the traceability table, the 26/27 status) — the requirement.
- `D:\ender\.planning\STATE.md` (5/8 phases complete, Phase 26 mid-execution, Phase 27 not started) — the scope-dependency fact.
- `D:\ender\server\tick_phases.go` (`tickOnce` lines 24–64, the single per-tick `Emit` at 59–61, the fixed phase pipeline, `tickAI`/`serverAiStep` call site) — the per-tick cost site.
- `D:\ender\plugin\host\emit.go` (`Emit` + the zero-subscriber fast path + `callHook` fresh-thread isolation) — the Emit-overhead bench target.
- `D:\ender\server\ai_mob.go` (`serverAiStep` lines 77–97, `newPigAI` lines 99–116) — the A/B benchmark target + the retained Go oracle.
- `D:\ender\server\vanilla_pig.go` (`spawnVanillaPig`/`spawnVanillaPigWithID`/`SetMobRegistry`) — the plugin-pig spawn path for the bench.
- `D:\ender\server\test_kit.go` (the `SULFUR_TEST_KIT` starter-kit pattern + `applyTestKit`) — the visual-gate trigger mechanism.
- `D:\ender\run-debug.sh` (the operator debug launch: online-mode + test-kit + persist + ultra-debug, seed 777) — the real-client harness.
- `D:\ender\world\noisegen_bench_test.go` (`BenchmarkGenerateOneChunk` — `b.ReportAllocs`/`b.ResetTimer`, the "can a real client be fed fast enough" gate framing) — the benchmark style template.
- `D:\ender\.planning\v1.0-MILESTONE-AUDIT.md` — the milestone-audit template (coverage table, cross-phase integration, build/test/-race, gaps, post-gate visual-approval note).
- Phase 23/24/25 SUMMARYs + the Phase 24 VERIFICATION — the retained-oracle decision (`TestPluginPigEqualsGoNativePig`, the Go goals KEPT), the interpreter-only-at-active-seam invariant (`TestNoInterpreterWhenIdle`), the `drainPendingPath` async-jitter discipline, the test-kit/`spawnDeclaredMob` seam, the deferred plugin-facing spawn builtin (T-23-11).
- `D:\ender\.planning\phases\27-folia-regionization\27-RESEARCH.md` (confirms Phase 27 is research-only/not-built; the perf benchmark + real-client gate are EXPLICITLY deferred to Phase 28; the regions-scale framing) — the conditional-scope fact for regions.

### Secondary (MEDIUM confidence)
- Go `testing` package conventions (`testing.B`, `b.N`, `b.Run`, `b.ReportAllocs`, `b.ReportMetric`) — standard, stable; matched to the project's existing bench style rather than asserted from external docs.
- `.planning/config.json` (`nyquist_validation: true`, `verifier: true`, `code_review: true`) — confirms the Validation Architecture section is required.

### Tertiary (LOW confidence — flagged for confirmation)
- The exact perf threshold values (Open Q1) — ILLUSTRATIVE only; must be set from a real baseline run.
- The exact 26/27 build state at Phase-28 execution time (Open Q2) — must be re-checked at plan time.
- The plugin-facing visible-output seam availability for the event gate (Assumption A5) — confirm at plan time (`log()` is the safe minimum).

## Metadata

**Confidence breakdown:**
- Standard stack (`testing.B`, no new dep): HIGH — the project already benchmarks this way; no library decision needed.
- Architecture (A/B against the retained oracle, the Emit bench, the visual script, the audit): HIGH — every named type (`serverAiStep`, `Emit`, `spawnVanillaPigWithID`, `newPigAI`, `drainPendingPath`, `newPhysicsLoop`, `installVanillaPigRegistry`, the test-kit pattern, the v1.0 audit template) was read from the live repo this session.
- Pitfalls: HIGH — the shared-nav-vs-delta, async-jitter, and oracle-retention pitfalls are grounded in the actual `TestPluginPigEqualsGoNativePig` design notes; the `autonomous:false` and 26/27-scope pitfalls are grounded in STATE.md + the requirement text.
- Perf threshold: LOW (intentionally deferred) — a project decision requiring a real baseline run (Open Q1).
- 26/27 conditional scope: MEDIUM — the conditionality is explicit in the requirement; the actual state must be re-checked at plan time (Open Q2).

**Research date:** 2026-06-28
**Valid until:** ~2026-07-28 (stable — the code map and gate design are anchored to verified phases 21–25; the only volatility is the 26/27 build state, which the plan re-checks, and the perf threshold, which is set at plan/discuss time).
