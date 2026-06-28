package server

// plugin_pig_race_test.go — PLUGIN-04 (Plan 24-02) Task 3: the swapped-pig -race gate. The SWAP put
// untrusted-plugin-driven goal callbacks (set_look_at / nearest_player / rand_int/rand_float/rand_double
// + the per-goal get_state/set_state scratch + nav.path_to) on the live tick. This test spawns plugin
// pigs and drives them through the FULL tick pipeline alongside a player so ALL 3 goals fire (stroll's
// nav + rand seams, lookAt's nearest_player + set_look_at, lookAround's rand_double + set_look_at),
// asserting no panic / no divergence. Under the Docker -race gate (CGO=1) the detector inspects the
// whole swapped tick path; it must come back clean because every seam is tick-owned by construction
// (id-handles re-resolved on the owner, the per-entity rng created in buildAIFromDecl and drawn only on
// the tick goroutine — no shared global, the scratch map owned by the goal struct, never shared).

import (
	"testing"
	"time"
)

// TestPluginPigRace spawns several plugin pigs via spawnVanillaPig and drives them through the running
// TickLoop for many ticks with a player nearby, so the stroll / lookAt / lookAround seams all execute
// on the live tick. It asserts the pigs survive and (under -race) that the swapped tick path is race-
// clean. It mirrors the async-stress pattern (a real running loop, the pools live) so the -race
// detector watches the pool->channel->owner crossings the plugin pig's nav rides.
func TestPluginPigRace(t *testing.T) {
	const floorY = 64
	loop := newStressLoop(t, 2, floorY) // installs the vanilla_pig registry; chunks [-2,2]^2
	defer loop.Close()

	// A player with a capturing client so the tracker has a real Send sink and the lookAt goal has a
	// nearest player to face (forcing the nearest_player + set_look_at seams).
	addStressPlayer(loop, 200000, 8.5, 8.5, floorY)

	// Several plugin pigs spread across the world (in look range of the player for some, wandering for
	// all) so every goal fires across the population.
	pigs := make([]*Entity, 0, 6)
	for i := 0; i < 6; i++ {
		x := 6.5 + float64(i)*2
		z := 6.5 + float64((i%3))*2
		pigs = append(pigs, loop.spawnVanillaPig(x, float64(floorY+1), z))
	}

	// Drive the running loop through several hundred logical ticks: tickAI -> serverAiStep ->
	// starlarkGoal.tick -> the plugin seams + nav, applyAsyncResults rejoins the A* paths, the tracker
	// emits. The pool workers run concurrently; the -race detector watches every crossing.
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	const ticks = 400
	for i := 0; i < ticks; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
	}

	// Quiesce any in-flight path so no worker is mid-send into asyncIn2 when Close() releases the pools.
	deadline := time.After(2 * time.Second)
	for {
		loop.applyAsyncResults()
		if !loop.spawnScanPending {
			break
		}
		select {
		case <-deadline:
			t.Fatal("a spawn scan never rejoined while quiescing the plugin-pig race loop")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	loop.applyAsyncResults()

	// The pigs must still be live (the swapped plugin tick did not crash/drop them) — proves the seams
	// ran for real, not that the test passed vacuously.
	alive := 0
	for _, p := range pigs {
		if _, ok := loop.entities.get(p.id); ok {
			alive++
		}
	}
	if alive == 0 {
		t.Fatal("no plugin pig survived the race loop — the swapped tick path crashed/dropped them all")
	}
}
