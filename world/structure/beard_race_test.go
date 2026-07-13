package structure

import (
	"sync"
	"testing"
)

// TestTerrainAdaptationForConcurrent is the regression guard for the `fatal error: concurrent map
// read and map write` that took the whole server down: the chunk-generation worker pool (ants) calls
// ForStructuresInChunk -> hasTerrainAdaptation -> terrainAdaptationFor from many goroutines at once
// for different chunks, and the memoization cache was a plain map (unguarded read + write). Go's
// map race detector is ALWAYS on (it is a runtime check, not the -race tooling), so this test panics
// the process the moment two goroutines hit the cache concurrently on an unguarded map — which is
// exactly what happened live. With the sync.Map fix it passes. It intentionally hammers a mix of
// cache-miss (fresh id) and cache-hit (repeated id) so both the Load and Store paths race.
func TestTerrainAdaptationForConcurrent(t *testing.T) {
	// A spread of ids: some resolve to real structures, most are unknown (adjNone) — either way the
	// cache stores + serves them. Repeats drive the hit path; distinct ids drive the miss/store path.
	ids := []string{
		"minecraft:village_plains", "minecraft:stronghold", "minecraft:desert_pyramid",
		"minecraft:igloo", "minecraft:jungle_pyramid", "minecraft:swamp_hut",
		"unknown:a", "unknown:b", "unknown:c", "unknown:d",
	}
	const goroutines = 64
	const iters = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Vary the id per (g,i) so cache misses and hits interleave across goroutines.
				_ = terrainAdaptationFor(ids[(g+i)%len(ids)])
				_ = hasTerrainAdaptation(ids[(g*i+1)%len(ids)])
			}
		}(g)
	}
	wg.Wait()
	// Reaching here without a `fatal error: concurrent map read and map write` IS the assertion.
}
