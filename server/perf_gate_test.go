package server

// perf_gate_test.go — PLUGIN-07 (Plan 28-01): the HARD, CI-repeatable PERF GATE. It asserts the
// plugin pig's per-(mob·tick) overhead vs the retained Phase-24 Go-native oracle stays within BOTH an
// ABSOLUTE ns cap AND a RELATIVE % cap — both set FROM a real baseline run on this machine, never
// guessed (CONTEXT D-3 / threat T-28-01: a too-loose cap lets a regression slip through).
//
// THIS REUSES THE PHASE-24 RETAINED ORACLE (newPigAI) and the drainPendingPath discipline
// (plugin_pig_test.go). The A/B pair is the SAME one TestPluginPigEqualsGoNativePig drives — here it
// is timed instead of compared. DO NOT delete the Go-native oracle (ai_mob.go newPigAI): it is the
// baseline the gate measures against. The equality (behavior-identity) is the OTHER proof; this is the
// perf proof.
//
// PITFALL 1 (DELTA, not absolute): both arms run the IDENTICAL Go nav + physics; the gate measures
// plugin_ns − go-native_ns per mob·tick (the isolated plugin tax), NOT the absolute per-tick cost.
//
// -race needs CGO=1 (Docker); this test (and the benches) run on the default CGO=0 host. The equality
// path is already -race-verified in Phase 24; this gate adds no cross-tick state.
//
// ─────────────────────────────────────────────────────────────────────────────────────────────────
// BASELINE (measured 2026-06-28, AMD Ryzen 7 5800X 8-Core, windows/amd64, Go default CGO=0 host):
//
//   go test -run TestPerfGate ./server/ -v   →   the avgTickNs wall-clock measure (100 mobs,
//   warmup=50, ticks=2000), THREE runs:
//       run 1: go-native=212679.5  plugin=215036.8  delta=2357.3 ns  (1.11%)
//       run 2: go-native=213062.8  plugin=217850.2  delta=4787.4 ns  (2.25%)
//       run 3: go-native=212943.0  plugin=216515.8  delta=3572.8 ns  (1.68%)
//     → per-(mob·tick) delta ≈ 2.4–4.8 µs ABSOLUTE, ≈ 1.1–2.25% RELATIVE.
//
//   The ABSOLUTE per-(mob·tick) number (~213 µs) is DOMINATED by drainPendingPath's blocking async-A*
//   round-trip latency (the off-tick worker rejoin), NOT by AI cost — so its run-to-run JITTER is high
//   and the absolute DELTA is noisy. The RELATIVE % is the stable, machine-portable plugin-tax signal
//   (a faster/slower CPU or async scheduler scales both arms together).
//
//   go test -bench BenchmarkPluginPigVsGoNative ./server/ -benchmem -count=3 (100 mobs/iter)
//   corroborated the SAME ~1% direction: go-native ≈14.95M ns/op, plugin ≈15.10M ns/op (≈1.0% over),
//   plugin allocs 10245 vs go-native 3436 per 100-mob iteration — the plugin tax is the starlark goal
//   call's per-tick allocation, NOT a per-tick CPU blowup.
//
// The caps below are set generously above the measured (noisy) baseline so async jitter never trips
// the gate, but tight enough that a real regression (a per-tick alloc storm in the plugin goal path,
// or the zero-subscriber Emit guard regressing) FAILS it. The RELATIVE cap is the primary gate; the
// ABSOLUTE cap is a coarse backstop sized well above the async-latency jitter.
//
//   maxDeltaNsPerMobTick — ABSOLUTE backstop: ~3× the max observed delta (driven by async jitter).
//   maxDeltaPct          — RELATIVE cap (primary): ~6–7× the median measured %, above the 2.25% peak.
//
// If this gate trips, FIRST re-read the baseline on the failing machine; a genuine plugin regression
// moves the RELATIVE %, not just the (jittery) absolute ns.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

import (
	"testing"
	"time"
)

// gateMobCount / gateWarmup / gateTicks are the gate's measurement parameters (RESEARCH Code
// Examples): 100 mobs, 50 warm-up ticks (drain first-call bias — Pitfall 2), 2000 timed ticks (a long
// window so the wall-clock per-(mob·tick) average is stable).
const (
	gateMobCount = 100
	gateWarmup   = 50
	gateTicks    = 2000
)

// ── Phase 30.1 RE-BASELINE (2026-06-30) ──────────────────────────────────────────────────────────
// The pre-30.1 baseline (1.1–2.25% delta) was measured against an effectively-IDLE plugin pig: the old
// RandomStrollGoal committed an UNREACHABLE underground target, so the stroll goal wedged "running"
// (canContinueToUse stayed true forever) and STOPPED re-rolling — the pig made very few Starlark calls
// after its first (wedged) stroll. Phase 30.1's faithful port makes the pig actually AMBLE: it now
//   (a) draws the FULL vanilla candidate stream per stroll — the probability nextFloat() + 10×
//       generateRandomDirection (30 nextInt), each an individual Starlark builtin call, vs the old
//       3 draws; PLUS the 30-float nav.path_to handoff (a 30-arg Starlark call) vs the old 3-arg;
//   (b) rolls the gate at the faithful reducedTickDelay(120)=60 (≈2× the old raw-120 cadence); and
//   (c) RE-ROLLS on arrival (the markArrived fix), so the goal cycles continuously instead of wedging.
// All of this is REQUIRED by the 1:1 jar mandate and runs identically on the Go-native arm — but on the
// Go arm it is compiled nextInt/array work, while on the plugin arm every draw + the 30-arg handoff is a
// Starlark interpreter call. So the plugin's per-(mob·tick) DELTA legitimately grew ~100× relative to the
// idle-wedged baseline. The snap itself is RNG-free Go shared by both arms (zero plugin delta). MEASURED
// (same machine, 3 runs, 100 mobs / warmup 50 / 2000 ticks): go-native ≈ 4.9–5.8 µs, plugin ≈ 16.7–18.5
// µs, delta ≈ 11.8–12.7 µs (≈ 214–243%). The caps below are set generously above that faithful baseline.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// maxDeltaNsPerMobTick is the ABSOLUTE per-(mob·tick) plugin-overhead cap (ns) — a COARSE backstop.
// Phase 30.1 faithful baseline: delta ≈ 11.8–12.7 µs/(mob·tick) (the per-stroll 30-draw + 30-arg
// path_to Starlark cost, ~2× cadence, continuous re-roll). Set at 30000 ns (~2.4× the max observed
// ~12.7 µs) so run-to-run jitter never trips it while a gross absolute regression still does.
const maxDeltaNsPerMobTick = 30000.0

// maxDeltaPct is the RELATIVE plugin-overhead cap (% over the go-native baseline) — the machine-portable
// signal (a faster/slower CPU/scheduler scales both arms together). Phase 30.1 faithful baseline:
// 214–243% (the active-pig Starlark draw/handoff tax — see the RE-BASELINE note above). Capped at 350%
// — comfortably above the 243% peak + noise, yet far below a "plugin regressed the per-tick path again"
// signal (e.g. a per-tick alloc storm or a re-introduced full-region scan would push it well past 350%).
const maxDeltaPct = 350.0

// avgTickNs builds the world via spawn(), warms up `warmup` ticks (Pitfall 2), then times `ticks`
// iterations of one full AI step (serverAiStep + drainPendingPath) over all spawned mobs with a
// wall-clock delta, returning the average ns per (mob·tick). The spawn callback returns the loop and
// the mobs so the same harness times both the go-native and the plugin arm with ZERO structural
// difference (only the AI builder differs — Pitfall 1).
func avgTickNs(tb testing.TB, spawn func() (*TickLoop, []*Entity), warmup, ticks int) float64 {
	tb.Helper()
	loop, mobs := spawn()
	if len(mobs) == 0 {
		tb.Fatal("avgTickNs: spawn returned no mobs")
	}
	// Warm up (drain lazy nav init + the first A* request out of the timed window).
	for i := 0; i < warmup; i++ {
		stepPigs(loop, mobs)
	}
	start := time.Now()
	for i := 0; i < ticks; i++ {
		stepPigs(loop, mobs)
	}
	elapsed := time.Since(start)
	return float64(elapsed.Nanoseconds()) / float64(ticks*len(mobs))
}

// TestPerfGate is the hard gate: it measures the go-native and plugin per-(mob·tick) cost over the
// SAME world + the SAME per-mob RNG seeds, then asserts the plugin DELTA is within BOTH the absolute
// ns cap and the relative % cap (set from the documented baseline). It t.Logf's the measured numbers
// so a CI run records the live baseline (and a near-trip is visible before it fails).
func TestPerfGate(t *testing.T) {
	// SKIP under the race detector: this is a WALL-CLOCK timing gate, and -race instruments every
	// memory access so the absolute ns/(mob·tick) balloons (~5-8×), tripping the absolute cap on
	// instrumentation overhead, not a real regression (the RELATIVE % stays well within cap). The file
	// header documents that this gate runs on the default CGO=0 host; the -race correctness pass runs
	// the equality oracle + nav/float tests (which carry no timing assertion). Guard so a full
	// `go test -race ./server/` is clean without a -run/-skip filter.
	if raceEnabled {
		t.Skip("perf gate is a wall-clock timing test; -race instrumentation inflates the absolute ns (run on the CGO=0 host)")
	}
	goNs := avgTickNs(t, func() (*TickLoop, []*Entity) {
		loop := buildPerfLoop(t)
		return loop, spawnGoNativePigs(loop, gateMobCount)
	}, gateWarmup, gateTicks)

	plNs := avgTickNs(t, func() (*TickLoop, []*Entity) {
		loop := buildPerfLoop(t)
		return loop, spawnPluginPigs(loop, gateMobCount)
	}, gateWarmup, gateTicks)

	deltaNs := plNs - goNs
	deltaPct := 100.0 * deltaNs / goNs

	t.Logf("perf gate (%d mobs, warmup=%d, ticks=%d): go-native=%.1f ns/(mob·tick)  plugin=%.1f ns/(mob·tick)  delta=%.1f ns (%.2f%%)",
		gateMobCount, gateWarmup, gateTicks, goNs, plNs, deltaNs, deltaPct)

	// A negative/zero delta (the plugin measured as fast as or faster than the oracle on this run) is
	// fine — it cannot exceed a positive cap. Only a delta OVER a cap is a regression.
	if deltaNs > maxDeltaNsPerMobTick {
		t.Fatalf("plugin per-(mob·tick) overhead %.1f ns exceeds the absolute cap %.1f ns (a regression in the plugin tick path; re-read the baseline if this is a slower machine)",
			deltaNs, maxDeltaNsPerMobTick)
	}
	if deltaPct > maxDeltaPct {
		t.Fatalf("plugin per-(mob·tick) overhead %.2f%% exceeds the relative cap %.2f%% (a regression in the plugin tick path)",
			deltaPct, maxDeltaPct)
	}
}
