package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// kelp_test.go -- the KELP random-tick gate (kelp.go). It proves:
//   - a kelp head below AGE 25, with water directly above, grows a new head one cell up (AGE+1) on a
//     nextDouble() < 0.14 roll;
//   - a roll >= 0.14 draws exactly one nextDouble and does NOT grow;
//   - no water above blocks the grow (still one draw).
// Uses the shared newRandomTickLoop / mustGet helpers.

func kelpState(age int) block.StateID {
	s, ok := block.KelpWithAge(block.ToStateID[block.Kelp{}], age)
	if !ok {
		panic("no kelp state")
	}
	return s
}

// findKelpSeed returns a seed whose FIRST NextDouble is < 0.14 (grow) if grow==true, else >= 0.14.
func findKelpSeed(grow bool) int64 {
	for seed := int64(1); seed < 100000; seed++ {
		r := levelgen.NewLegacyRandomSource(seed)
		d := r.NextDouble()
		if (d < block.KelpGrowChance) == grow {
			return seed
		}
	}
	panic("no kelp seed found")
}

// TestKelpGrowsUpOnChance: a kelp head with water above grows a new head one cell up (AGE+1) when the
// nextDouble roll is < 0.14. CITE: KelpBlock / GrowingPlantHeadBlock.randomTick.
func TestKelpGrowsUpOnChance(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	head := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(head, kelpState(3), dimMinY)
	// Water directly above (the grow target must be the strict WATER block).
	mgr.SetBlock(above(head), block.ToStateID[block.Water{}], dimMinY)

	r.levelRandom = levelgen.NewLegacyRandomSource(findKelpSeed(true))
	loop.kelpRandomTick(r, kelpState(3), head)

	// The new head should be at above(head) with AGE 4; the old head stays at head (AGE 3).
	if got := block.KelpAge(mustGet(t, mgr, above(head))); got != 4 {
		t.Fatalf("grown kelp head AGE = %d, want 4 (grew up on a <0.14 roll)", got)
	}
	if got := block.KelpAge(mustGet(t, mgr, head)); got != 3 {
		t.Fatalf("old kelp head AGE = %d, want 3 (unchanged this tick)", got)
	}
}

// TestKelpNoGrowAboveChance: a roll >= 0.14 draws EXACTLY one nextDouble and does not grow. The cell
// above stays water. CITE: GrowingPlantHeadBlock.randomTick (nextDouble() < prob).
func TestKelpNoGrowAboveChance(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	head := pk.Position{X: 6, Y: 65, Z: 6}
	mgr.SetBlock(head, kelpState(3), dimMinY)
	water := block.ToStateID[block.Water{}]
	mgr.SetBlock(above(head), water, dimMinY)

	seed := findKelpSeed(false)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)
	loop.kelpRandomTick(r, kelpState(3), head)

	// The cell above must still be plain water (no growth).
	if got := mustGet(t, mgr, above(head)); got != water {
		t.Fatalf("kelp grew on a >=0.14 roll (above = %d, want water %d)", got, water)
	}
	// Exactly one nextDouble was drawn: the stream after the handler equals a fresh stream advanced by
	// one nextDouble.
	fresh := levelgen.NewLegacyRandomSource(seed)
	fresh.NextDouble()
	if r.levelRandom.NextDouble() != fresh.NextDouble() {
		t.Fatal("kelp no-grow path drew != 1 nextDouble")
	}
}

// TestKelpNoWaterAboveBlocksGrow: a <0.14 roll still does NOT grow when the cell above is not the
// WATER block (canGrowInto fails). The draw is still consumed (drawn before the canGrowInto read).
// CITE: KelpBlock.canGrowInto (is(Blocks.WATER)).
func TestKelpNoWaterAboveBlocksGrow(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()

	head := pk.Position{X: 7, Y: 65, Z: 7}
	mgr.SetBlock(head, kelpState(3), dimMinY)
	// Above is air (EmptyChunk default): canGrowInto(air) == false.

	r.levelRandom = levelgen.NewLegacyRandomSource(findKelpSeed(true))
	loop.kelpRandomTick(r, kelpState(3), head)

	if !block.IsAir(mustGet(t, mgr, above(head))) {
		t.Fatalf("kelp grew into a non-water cell; above = %d", mustGet(t, mgr, above(head)))
	}
}
