package server

// plugin_perf_bench_test.go — PLUGIN-07 (Plan 28-01): the A/B PERF BENCH that isolates the plugin
// pig's per-tick overhead vs the retained Phase-24 Go-native oracle, plus the per-tick Emit-overhead
// bench (the zero-subscriber hot path is allocation-free).
//
// THIS REUSES THE PHASE-24 RETAINED ORACLE (newPigAI) and the drainPendingPath discipline from
// plugin_pig_test.go — the SAME A/B pair the equality test (TestPluginPigEqualsGoNativePig) drives,
// promoted from a correctness proof to a perf measurement. DO NOT delete the Go-native oracle
// (newPigAI / ai_mob.go): it is the baseline the plugin delta is measured against.
//
// PITFALL 1 (report the DELTA, never the absolute): both sub-benches run the IDENTICAL Go nav +
// physics; the ONLY difference is the AI builder (Go newPigAI vs the plugin declaration's starlark
// goals). So the meaningful number is plugin_ns/op MINUS go-native_ns/op — the isolated plugin
// overhead. The absolute ns/op includes the (shared) nav/physics cost and is NOT the plugin tax.
//
// PITFALL 2 (first-call bias): the first serverAiStep of a freshly-spawned mob pays one-time costs
// (lazy nav init, the first A* request). Each sub-bench WARMS UP a handful of ticks (with
// drainPendingPath) BEFORE b.ResetTimer() so the timed loop measures the steady-state per-tick cost.
//
// -race needs CGO=1 (Docker); these benchmarks run on the default CGO=0 host. The equality path is
// already -race-verified in Phase 24 — the bench adds no cross-tick state (the loops are tick-owned).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
)

// perfFloorY is the floor height the A/B benches build their pigs on (matches the equality test).
const perfFloorY = 64

// perfMobCount is the number of mobs each A/B sub-bench drives per timed iteration. 100 mobs (the
// RESEARCH Code Examples value) amortizes per-call timing noise into a stable per-tick aggregate.
const perfMobCount = 100

// buildPerfLoop builds a physics loop with a 3×3 floor (so a strolling pig always has ground to path
// over) and a player east of the spawn within look range (so the lookAt goal fires for both the Go
// and plugin pigs — the same world the equality test uses). Returns the loop.
func buildPerfLoop(tb testing.TB) *TickLoop {
	tb.Helper()
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, perfFloorY)
		}
	}
	// SHARE ONE entity store across every region (mirroring newPhysicsLoop's shared-world wiring). The
	// bench drives serverAiStep + drainPendingPath DIRECTLY on the coordinator goroutine (the equality-
	// test discipline), bypassing the full tick's region-transfer barrier. As a strolling pig crosses a
	// chunk-column boundary its regionForEntity (used to build the goal's region-bound handles) flips to
	// the other region, but the direct-step path never transfers it between region stores — so without a
	// shared store the goal callback's handle would resolve against a region whose store does not hold
	// the pig ("entity N no longer exists"), silently no-opping the plugin goal and CORRUPTING the
	// measured plugin cost (a failing callback is cheaper than a succeeding one). Aliasing the stores
	// makes regionForEntity and only() always resolve the SAME store, so the plugin goal runs its real
	// per-tick work regardless of which column the pig wanders into. This changes NO AI/RNG/physics — it
	// only removes the direct-step harness's region-transfer gap (the production tick handles transfer
	// at the barrier; the perf number is identical either way once the callback actually runs).
	for _, r := range loop.regions {
		r.entities = loop.regions[globalRegion].entities
	}
	// A player east of the spawn cluster within the 6.0 look distance so the lookAt goal runs.
	loop.players = append(loop.players, &tickPlayer{x: 11.5, y: float64(perfFloorY + 1), z: 8.5})
	return loop
}

// spawnGoNativePigs spawns N Go-native oracle pigs (newPigAI + reseedMobAI) with distinct ids. This is
// the retained Phase-24 baseline (DO NOT delete newPigAI).
func spawnGoNativePigs(loop *TickLoop, n int) []*Entity {
	pigs := make([]*Entity, 0, n)
	for i := 0; i < n; i++ {
		id := int32(1000 + i)
		pig := NewEntity(id, entity.Pig, 8.5, float64(perfFloorY+1), 8.5)
		pig.ai = newPigAI()
		reseedMobAI(pig.ai, id)
		loop.only().entities.add(pig)
		pigs = append(pigs, pig)
	}
	return pigs
}

// spawnPluginPigs spawns N plugin-driven pigs (spawnVanillaPigWithID, the boot-loaded declaration)
// with the SAME distinct ids the Go-native set uses (so the reseed-derived RNG streams match). This is
// the A side; spawnGoNativePigs is the B side.
func spawnPluginPigs(loop *TickLoop, n int) []*Entity {
	pigs := make([]*Entity, 0, n)
	for i := 0; i < n; i++ {
		id := int32(1000 + i)
		pig := loop.spawnVanillaPigWithID(id, 8.5, float64(perfFloorY+1), 8.5)
		pigs = append(pigs, pig)
	}
	return pigs
}

// stepPigs drives one full AI tick over every pig: serverAiStep then a deterministic drainPendingPath
// (the equality test's discipline — rejoin the async A* the same tick so the bench measures the AI/RNG
// cost, not the off-tick scheduler). This is the unit of work both sub-benches time.
func stepPigs(loop *TickLoop, pigs []*Entity) {
	for _, p := range pigs {
		p.ai.serverAiStep(loop, p)
		drainPendingPath(loop, p)
	}
}

// warmPigs runs `ticks` warm-up steps before timing (Pitfall 2 — drain the first-call costs out).
func warmPigs(loop *TickLoop, pigs []*Entity, ticks int) {
	for i := 0; i < ticks; i++ {
		stepPigs(loop, pigs)
	}
}

// BenchmarkPluginPigVsGoNative is the A/B bench: two sub-benches over the IDENTICAL world + the SAME
// per-mob RNG seeds, differing ONLY in the AI builder. The plugin sub-bench ns/op MINUS the go-native
// sub-bench ns/op = the isolated plugin overhead per mob·tick·perfMobCount (Pitfall 1 — read the
// delta, not the absolute). Both report allocs.
func BenchmarkPluginPigVsGoNative(b *testing.B) {
	b.Run("go-native", func(b *testing.B) {
		loop := buildPerfLoop(b)
		pigs := spawnGoNativePigs(loop, perfMobCount)
		warmPigs(loop, pigs, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stepPigs(loop, pigs)
		}
	})
	b.Run("plugin", func(b *testing.B) {
		loop := buildPerfLoop(b)
		pigs := spawnPluginPigs(loop, perfMobCount)
		warmPigs(loop, pigs, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stepPigs(loop, pigs)
		}
	})
}

// BenchmarkServerAiStepPluginVsGo is the SINGLE-mob A/B: the per-serverAiStep delta for ONE plugin pig
// vs ONE Go pig, for an isolated per-mob number (perfMobCount=100 aggregates; this one isolates the
// per-mob·tick cost so the gate's absolute ns/(mob·tick) cap is directly readable here too).
func BenchmarkServerAiStepPluginVsGo(b *testing.B) {
	b.Run("go-native", func(b *testing.B) {
		loop := buildPerfLoop(b)
		pigs := spawnGoNativePigs(loop, 1)
		warmPigs(loop, pigs, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stepPigs(loop, pigs)
		}
	})
	b.Run("plugin", func(b *testing.B) {
		loop := buildPerfLoop(b)
		pigs := spawnPluginPigs(loop, 1)
		warmPigs(loop, pigs, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			stepPigs(loop, pigs)
		}
	})
}

// BenchmarkTickEmitOverhead measures the per-tick host.Manager.Emit cost on the two paths the on_tick
// funnel takes:
//
//   - "zero-subscribers": no hooks registered → Emit hits the len(hooks)==0 fast path (one map read,
//     ZERO allocation), the cost a server with no plugin pays per tick. This sub-bench's ~0 allocs/op
//     is the proof that the unsubscribed hot path is free (the must_have).
//   - "one-subscriber": ONE trivial on_tick hook registered → Emit builds the starlark args and fires
//     the hook on a fresh thread. This documents the SUBSCRIBED cost (NOT on the default path).
func BenchmarkTickEmitOverhead(b *testing.B) {
	b.Run("zero-subscribers", func(b *testing.B) {
		m := host.New() // no plugin loaded → m.hooks[on_tick] is empty
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			m.Emit(host.EventTick, host.TickEvent{Tick: n})
		}
	})
	b.Run("one-subscriber", func(b *testing.B) {
		m := loadOnTickPlugin(b)
		if m.HookCount(host.EventTick) != 1 {
			b.Fatalf("expected exactly 1 on_tick hook, got %d", m.HookCount(host.EventTick))
		}
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			m.Emit(host.EventTick, host.TickEvent{Tick: n})
		}
	})
}

// loadOnTickPlugin materializes a tiny on_tick plugin (a no-op hook) to a temp dir and loads it
// through the EXPORTED host.Manager.LoadDir, returning a Manager with exactly one on_tick subscriber —
// the one-subscriber Emit bench's setup. Uses only the exported host API (the host package's internal
// greeter helpers are not visible from package server).
func loadOnTickPlugin(b *testing.B) *host.Manager {
	b.Helper()
	root := b.TempDir()
	dir := filepath.Join(root, "ticker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("mkdir plugin dir: %v", err)
	}
	manifest := "" +
		"name = \"ticker\"\n" +
		"version = \"0.1.0\"\n" +
		"entrypoint = \"main.star\"\n" +
		"runtime = \"starlark\"\n" +
		"capabilities = [\"events\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		b.Fatalf("write manifest: %v", err)
	}
	// A trivial on_tick hook: register a no-op so Emit has exactly one subscriber.
	star := "" +
		"def on_tick(tick):\n" +
		"    pass\n" +
		"register(\"on_tick\", on_tick)\n"
	if err := os.WriteFile(filepath.Join(dir, "main.star"), []byte(star), 0o644); err != nil {
		b.Fatalf("write main.star: %v", err)
	}
	m := host.New()
	if err := m.LoadDir(root); err != nil {
		b.Fatalf("LoadDir(ticker): %v", err)
	}
	return m
}
