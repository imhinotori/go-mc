package world

import (
	"fmt"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// These tests pin BlockColumnFeature's draw order (per-layer height sample, then the
// positional allowed_placement scan, then per-cell provider draws) and its placement.

func columnCF(t *testing.T, config string) *feature.ConfiguredFeature {
	reg := feature.NewEmbeddedRegistry()
	obj := fmt.Sprintf(`{"type":"minecraft:block_column","config":%s}`, config)
	cf, err := reg.ParseConfiguredFeature("minecraft:test_block_column", []byte(obj))
	if err != nil {
		t.Fatalf("ParseConfiguredFeature(block_column): %v", err)
	}
	return cf
}

// TestBlockColumnUp: a two-layer column growing UP (a stem layer of constant height 3 + a
// tip layer of height 1), placed into air (allowed_placement = matching_block_tag air).
// Assert the stem cells + the tip cell land in order.
func TestBlockColumnUp(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 4, Y: 60, Z: 4}
	// Column is written STARTING AT the origin, moving up. The scan starts at origin+up.
	stem := `{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:bamboo_planks"}}`
	tip := `{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:oak_planks"}}`
	cfg := fmt.Sprintf(`{"allowed_placement":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"},"direction":"up","layers":[{"height":3,"provider":%s},{"height":1,"provider":%s}],"prioritize_tip":true}`, stem, tip)
	cf := columnCF(t, cfg)

	rng := levelgen.NewWorldgenRandom(7)
	if !blockColumnBody(bctx, cf, newPlacementContext(bctx.view, -64, 384, nil), rng, origin) {
		t.Fatalf("block_column up returned false")
	}

	stemState := block.ToStateID[block.BambooPlanks{}]
	tipState := block.ToStateID[block.OakPlanks{}]
	// 3 stem cells at origin.Y..origin.Y+2, tip at origin.Y+3.
	for dy := 0; dy < 3; dy++ {
		if got := view.GetBlock(origin.X, origin.Y+dy, origin.Z); got != stemState {
			t.Fatalf("stem cell %d missing: got %d want %d", dy, got, stemState)
		}
	}
	if got := view.GetBlock(origin.X, origin.Y+3, origin.Z); got != tipState {
		t.Fatalf("tip cell missing: got %d want %d", got, tipState)
	}
}

// TestBlockColumnDown: a column growing DOWN (cave-vines shape) writes from origin
// downward. Assert the cells land at origin.Y, origin.Y-1, ...
func TestBlockColumnDown(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 6, Y: 60, Z: 6}
	body := `{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:bamboo_planks"}}`
	cfg := fmt.Sprintf(`{"allowed_placement":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"},"direction":"down","layers":[{"height":4,"provider":%s}],"prioritize_tip":true}`, body)
	cf := columnCF(t, cfg)

	rng := levelgen.NewWorldgenRandom(3)
	if !blockColumnBody(bctx, cf, newPlacementContext(bctx.view, -64, 384, nil), rng, origin) {
		t.Fatalf("block_column down returned false")
	}
	state := block.ToStateID[block.BambooPlanks{}]
	for dy := 0; dy < 4; dy++ {
		if got := view.GetBlock(origin.X, origin.Y-dy, origin.Z); got != state {
			t.Fatalf("down cell %d missing at y=%d: got %d want %d", dy, origin.Y-dy, got, state)
		}
	}
}

// TestBlockColumnTruncate: with a blocking (non-air) cell partway up, the column is
// truncated so it does not overwrite the blocker. The scan stops at the first disallowed
// cell; prioritize_tip=true trims from the tip (layer 0 here) inward.
func TestBlockColumnTruncate(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 2, Y: 60, Z: 2}
	// Put a stone blocker 2 cells above origin (origin+up scan cells are origin.Y+1,
	// origin.Y+2, ...). The scan cell origin.Y+2 is stone -> allowed(air) fails at y index 1.
	stone := block.ToStateID[block.Stone{}]
	view.SetBlock(origin.X, origin.Y+2, origin.Z, stone)

	body := `{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:bamboo_planks"}}`
	cfg := fmt.Sprintf(`{"allowed_placement":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"},"direction":"up","layers":[{"height":5,"provider":%s}],"prioritize_tip":true}`, body)
	cf := columnCF(t, cfg)

	rng := levelgen.NewWorldgenRandom(9)
	blockColumnBody(bctx, cf, newPlacementContext(bctx.view, -64, 384, nil), rng, origin)

	state := block.ToStateID[block.BambooPlanks{}]
	// totalHeight 5, scan finds the blocker at y index 1 (origin.Y+2 in the scan), so
	// newHeight=1: prioritize_tip removes 4 from the single layer -> 1 cell placed at origin.
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != state {
		t.Fatalf("truncated column did not place its surviving cell at origin: got %d", got)
	}
	// The stone blocker must remain (not overwritten).
	if got := view.GetBlock(origin.X, origin.Y+2, origin.Z); got != stone {
		t.Fatalf("truncated column overwrote the blocker: got %d want %d", got, stone)
	}
}

// TestBlockColumnCaveVineWeightedHeight: the real cave_vine config (weighted_list height +
// randomized_int tip provider, direction down) parses and places, and is deterministic for
// a fixed seed. Uses the embedded cave_vine configured_feature.
func TestBlockColumnCaveVine(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:cave_vine")
	if err != nil {
		t.Skipf("cave_vine not resolvable in embedded registry: %v", err)
	}
	run := func() (int64, []block.StateID) {
		bctx, view := vegBctx()
		bctx.reg = reg
		origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
		rng := levelgen.NewWorldgenRandom(0xCA7E)
		blockColumnBody(bctx, cf, newPlacementContext(bctx.view, -64, 384, nil), rng, origin)
		var snap []block.StateID
		for y := 40; y <= 70; y++ {
			snap = append(snap, view.GetBlock(8, y, 8))
		}
		return rng.NextLong(), snap
	}
	f1, s1 := run()
	f2, s2 := run()
	if f1 != f2 {
		t.Fatalf("cave_vine block_column non-deterministic rng fingerprint: %d vs %d", f1, f2)
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("cave_vine block_column non-deterministic at index %d", i)
		}
	}
	// Assert at least one cave_vines(_plant) cell landed downward from the origin.
	placed := 0
	_, snap := run()
	for _, st := range snap {
		if st != 0 {
			placed++
		}
	}
	if placed == 0 {
		t.Fatalf("cave_vine block_column placed no cells over air")
	}
}
