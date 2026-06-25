package structure

import (
	"fmt"
	"testing"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// visual_gate_coords_test.go prints the VISUAL GATE coordinates the Phase-16 human-verify gate
// (16-03 Task 2) needs: for the fixed gate seed, the world XZ of a near-spawn village + one of
// each prior-tier structure, DERIVED from the placement math (the village random_spread chunk,
// the stronghold concentric-ring positions), NOT eyeballed. The orchestrator copies these into
// the visual-gate checklist so the human can /tp directly to each structure.
//
// The gate seed is 25: it lands a SNOWY village 6 chunks from spawn (chunk (1,6) ≈ world
// (24,104)) — the nearest-spawn village across the low seeds probed — plus an igloo, a
// mineshaft, and a stronghold all reachable. (Seed 0's plains village at chunk (15,2) is the
// router-independent ACCEPTANCE anchor; seed 25 is the live-client gate anchor because its
// village biome-gates IN near spawn under the real router.)
const visualGateSeed = int64(25)

// TestVisualGateCoords prints (and lightly verifies) the gate coordinates. It is NOT a pass/fail
// contract on the world (that is the human gate); it derives + prints the coords so the run log
// carries them. The village chunk + the stronghold ring are re-derived here from the algorithm.
func TestVisualGateCoords(t *testing.T) {
	const cx, cz = 1, 6 // the seed-25 snowy village owning chunk (probed via the live pipeline)
	// Verify (1,6) IS the algorithm's village structure chunk for seed 25 (the placement gate).
	vp := RandomSpreadStructurePlacement{Spacing: 34, Separation: 8, Salt: 10387312, SpreadType: SpreadLinear}
	if !vp.IsStructureChunk(visualGateSeed, cx, cz) {
		t.Fatalf("seed-25 village anchor (%d,%d) is not the algorithm's structure chunk", cx, cz)
	}
	wx, wz := cx*16+8, cz*16+8

	// The stronghold nearest-spawn ring chunk, re-derived from the concentric-ring algorithm.
	preferred, err := LoadStrongholdBiasedTo()
	if err != nil {
		t.Fatalf("LoadStrongholdBiasedTo: %v", err)
	}
	biomeAt := func(int, int, int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }
	rs := NewStrongholdRingState(visualGateSeed, biomeAt, preferred)
	shBest := rs.RingPositions()[0]
	shD := chebyshev(int(shBest[0]), int(shBest[1]))
	for _, p := range rs.RingPositions() {
		if d := chebyshev(int(p[0]), int(p[1])); d < shD {
			shD, shBest = d, p
		}
	}

	// The prior-tier structures' near-spawn owning chunks for seed 25 (derived from the live
	// pipeline scan during plan execution; the igloo/mineshaft are random_spread/legacy-freq
	// placements that the human reaches by exploring or /tp). Printed for the gate checklist.
	fmt.Printf("VISUAL GATE seed: %d, village (snowy) at chunk (%d,%d) ~ world (%d,%d)\n", visualGateSeed, cx, cz, wx, wz)
	fmt.Printf("VISUAL GATE seed: %d, igloo (temple) at chunk (-9,-18) ~ world (-136,-280)\n", visualGateSeed)
	fmt.Printf("VISUAL GATE seed: %d, mineshaft (underground) at chunk (7,-4) ~ world (120,-56)\n", visualGateSeed)
	fmt.Printf("VISUAL GATE seed: %d, stronghold (deep underground) at ring chunk (%d,%d) ~ world (%d,%d)\n",
		visualGateSeed, shBest[0], shBest[1], int(shBest[0])*16+8, int(shBest[1])*16+8)
	fmt.Printf("VISUAL GATE seed: %d, desert pyramid (DISTANT) at chunk (310,271) ~ world (4968,4344)\n", visualGateSeed)
}
