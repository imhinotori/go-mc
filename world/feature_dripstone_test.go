package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_dripstone_test.go pins the speleothem / speleothem_cluster / large_dripstone bodies
// (SpeleothemFeature / SpeleothemClusterFeature / LargeDripstoneFeature, javap -c / CFR against
// 26.2-inner.jar). Each is exercised over a synthetic cave (solid stone floor + ceiling with an
// air gap) so the Column.scan finds real floor/ceiling edges and the pointed-dripstone /
// dripstone_block placement branches fire. The determinism contract: a seed-matched re-run is
// bit-identical.

func resolveDripstoneCF(t *testing.T, id string) *feature.ConfiguredFeature {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured(id)
	if err != nil {
		t.Fatalf("ResolveConfigured(%s): %v", id, err)
	}
	return cf
}

// buildCave fills the whole 3x3 with stone over [minY, floorY] and [ceilY, topY], leaving an
// air gap (floorY, ceilY) — a cavern the dripstone columns can scan and grow into.
func buildCave(center [2]int, minY, height, floorY, ceilY, topY int) *Neighborhood {
	view := build3x3(center, minY, height)
	stone := block.ToStateID[block.Stone{}]
	air := block.ToStateID[block.Air{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= topY; y++ {
						x, z := bx+lx, bz+lz
						if y <= floorY || y >= ceilY {
							view.SetBlock(x, y, z, stone)
						} else {
							view.SetBlock(x, y, z, air)
						}
					}
				}
			}
		}
	}
	return view
}

func TestSpeleothemPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveDripstoneCF(t, "minecraft:pointed_dripstone")
	// pointed_dripstone is a simple_random_selector whose sub-feature is "speleothem"; call the
	// speleothem body directly with the resolved sub-feature config to pin the leaf feature.
	sub := resolveDripstoneCF(t, "minecraft:pointed_dripstone")
	_ = cf
	_ = sub

	// Set up a ceiling of dripstone_block with air below: the origin sits just under the
	// ceiling, so canPlaceAbove (base) holds → tip points DOWN and a stalactite grows down.
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0xD819_5701)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// Ceiling of dripstone_block at origin.Y+1 (and above), air at/below origin.
		drip := block.ToStateID[block.DripstoneBlock{}]
		for dy := 1; dy <= 4; dy++ {
			view.SetBlock(origin.X, origin.Y+dy, origin.Z, drip)
		}
		return view
	}

	// Drive the "speleothem" body via the selector so the sub-feature config is resolved from
	// the embedded pointed_dripstone.json. We invoke the leaf body directly using the config
	// parsed from the inline sub-feature.
	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	leafCF := inlineSpeleothemCF(t)
	ok := speleothemBody(bctx, leafCF, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("speleothem body returned false under a base ceiling")
	}
	pointed := countStatesOfBlock(view, origin, "minecraft:pointed_dripstone", 3, origin.Y+4)
	if pointed == 0 {
		t.Fatalf("speleothem placed no pointed_dripstone blocks")
	}

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	speleothemBody(bctx2, leafCF, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 4, origin.Y-4, origin.Y+4)
}

// inlineSpeleothemCF builds a "speleothem" ConfiguredFeature with the same config the
// pointed_dripstone.json sub-features carry (base=dripstone_block, pointed=pointed_dripstone,
// replaceable=#dripstone_replaceable_blocks). This mirrors the inline sub-feature the selector
// resolves, letting the test drive the leaf body directly.
func inlineSpeleothemCF(t *testing.T) *feature.ConfiguredFeature {
	t.Helper()
	raw := []byte(`{
		"base_block": {"Name": "minecraft:dripstone_block"},
		"pointed_block": {"Name": "minecraft:pointed_dripstone", "Properties": {"thickness": "tip", "vertical_direction": "up", "waterlogged": "false"}},
		"replaceable_blocks": "#minecraft:dripstone_replaceable_blocks"
	}`)
	return &feature.ConfiguredFeature{
		ID:     "minecraft:__test_speleothem",
		Type:   "speleothem",
		Config: &feature.ParsedConfig{Raw: raw},
	}
}

func TestSpeleothemClusterPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveDripstoneCF(t, "minecraft:dripstone_cluster")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0x0C1_057E7)

	// A cave with floor at y=32 and ceiling at y=48 (air gap 33..47); origin at y=40 is air.
	build := func() *Neighborhood { return buildCave(center, minY, height, 32, 48, origin.Y+30) }

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := speleothemClusterBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("speleothem_cluster body returned false over a valid cave")
	}
	pointed := countClusterStates(view, origin, "minecraft:pointed_dripstone")
	if pointed == 0 {
		t.Fatalf("speleothem_cluster placed no pointed_dripstone blocks")
	}

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	speleothemClusterBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 12, 30, 50)
}

func TestLargeDripstonePlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveDripstoneCF(t, "minecraft:large_dripstone")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const seed = int64(0x1A26_D819)

	// A tall cave: floor at y=28, ceiling at y=52 (height 23 >= 4), origin at y=40.
	build := func() *Neighborhood { return buildCave(center, minY, height, 28, 52, origin.Y+40) }

	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	ok := largeDripstoneBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("large_dripstone body returned false over a valid tall cave")
	}
	drip := countClusterStates(view, origin, "minecraft:dripstone_block")
	if drip == 0 {
		t.Fatalf("large_dripstone placed no dripstone_block cells")
	}

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	largeDripstoneBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 18, 26, 54)
}

// countClusterStates counts cells over a wide box around origin holding a state of block `want`.
func countClusterStates(view *Neighborhood, origin placement.BlockPos, want string) int {
	n := 0
	for dx := -18; dx <= 18; dx++ {
		for dz := -18; dz <= 18; dz++ {
			for y := 24; y <= 56; y++ {
				st := view.GetBlock(origin.X+dx, y, origin.Z+dz)
				if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == want {
					n++
				}
			}
		}
	}
	return n
}
