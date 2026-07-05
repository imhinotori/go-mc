package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_mushroom_test.go pins the huge_red_mushroom / huge_brown_mushroom bodies
// (AbstractHugeMushroomFeature.place + the red/brown makeCap, javap -c 26.2-inner.jar): a
// valid position over a can_place_on block with clear air above grows a stem + cap; an
// invalid position (nothing to place on) returns false with no blocks; the body is a pure
// function of (rng, view) — a re-run on a seed-matched view is bit-identical.

// fillMushroomGround places a can_place_on block (mycelium — in #substrate_overworld via
// #grass_blocks) at world (x, y, z) so the origin ABOVE it validates.
func setMycelium(view *Neighborhood, x, y, z int) {
	view.SetBlock(x, y, z, block.ToStateID[block.Mycelium{}])
}

func resolveMushroomCF(t *testing.T, id string) *feature.ConfiguredFeature {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured(id)
	if err != nil {
		t.Fatalf("ResolveConfigured(%s): %v", id, err)
	}
	return cf
}

// countStatesOfBlock counts, over the 3x3 view Y-range around origin, how many cells hold a
// state whose block id matches want.
func countStatesOfBlock(view *Neighborhood, origin placement.BlockPos, want string, reach, yHi int) int {
	n := 0
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			for y := origin.Y - 1; y <= yHi; y++ {
				st := view.GetBlock(origin.X+dx, y, origin.Z+dz)
				if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == want {
					n++
				}
			}
		}
	}
	return n
}

func TestHugeRedMushroomPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveMushroomCF(t, "minecraft:huge_red_mushroom")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0x5EED_A11)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		setMycelium(view, origin.X, origin.Y-1, origin.Z)
		return view
	}

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := hugeRedMushroomBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("huge_red_mushroom body returned false over a valid ground")
	}

	stems := countStatesOfBlock(view, origin, "minecraft:mushroom_stem", 4, origin.Y+18)
	caps := countStatesOfBlock(view, origin, "minecraft:red_mushroom_block", 4, origin.Y+18)
	if stems == 0 {
		t.Fatalf("huge_red_mushroom placed no stem blocks")
	}
	if caps == 0 {
		t.Fatalf("huge_red_mushroom placed no cap (red_mushroom_block) blocks")
	}

	// Determinism: a seed-matched re-run is bit-identical.
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	hugeRedMushroomBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 5, origin.Y-1, origin.Y+18)
}

func TestHugeBrownMushroomPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveMushroomCF(t, "minecraft:huge_brown_mushroom")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0xB40_1234)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		setMycelium(view, origin.X, origin.Y-1, origin.Z)
		return view
	}

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := hugeBrownMushroomBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("huge_brown_mushroom body returned false over a valid ground")
	}
	stems := countStatesOfBlock(view, origin, "minecraft:mushroom_stem", 4, origin.Y+18)
	caps := countStatesOfBlock(view, origin, "minecraft:brown_mushroom_block", 4, origin.Y+18)
	if stems == 0 {
		t.Fatalf("huge_brown_mushroom placed no stem blocks")
	}
	if caps == 0 {
		t.Fatalf("huge_brown_mushroom placed no cap (brown_mushroom_block) blocks")
	}

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	hugeBrownMushroomBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 5, origin.Y-1, origin.Y+18)
}

func TestHugeMushroomInvalidGroundNoPlace(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveMushroomCF(t, "minecraft:huge_red_mushroom")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	// No ground block below → can_place_on fails → isValidPosition false → no placement.
	view := build3x3(center, minY, height)
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	if hugeRedMushroomBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("huge_red_mushroom placed over air ground (expected no-op)")
	}
	if countStatesOfBlock(view, origin, "minecraft:mushroom_stem", 4, origin.Y+18) != 0 {
		t.Fatalf("huge_red_mushroom wrote blocks despite invalid position")
	}
}

// assertCaveViewsEqual asserts two 3x3 views hold identical states over a box around origin.
func assertCaveViewsEqual(t *testing.T, a, b *Neighborhood, origin placement.BlockPos, reach, yLo, yHi int) {
	t.Helper()
	for dx := -reach; dx <= reach; dx++ {
		for dz := -reach; dz <= reach; dz++ {
			for y := yLo; y <= yHi; y++ {
				x, z := origin.X+dx, origin.Z+dz
				if a.GetBlock(x, y, z) != b.GetBlock(x, y, z) {
					t.Fatalf("determinism mismatch at (%d,%d,%d): %d != %d", x, y, z,
						a.GetBlock(x, y, z), b.GetBlock(x, y, z))
				}
			}
		}
	}
}
