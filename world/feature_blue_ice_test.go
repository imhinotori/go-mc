package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// blueIceCF is an empty-config blue_ice configured feature (NoneFeatureConfiguration).
func blueIceCF() *feature.ConfiguredFeature {
	return &feature.ConfiguredFeature{Type: "blue_ice", Config: &feature.ParsedConfig{Raw: []byte(`{}`)}}
}

// buildOceanFloor fills the 3x3 with water above a deep floor and puts a packed_ice pillar next
// to the origin so the blue_ice gate (packed_ice neighbour + water) passes.
func buildOceanFloor(minY, height, floorY, waterTop int) *Neighborhood {
	view := build3x3([2]int{0, 0}, minY, height)
	stone := block.ToStateID[block.Stone{}]
	water := block.ToStateID[block.Water{Level: 0}]
	packed := block.ToStateID[block.PackedIce{}]
	for wx := -16; wx <= 31; wx++ {
		for wz := -16; wz <= 31; wz++ {
			for y := minY; y <= floorY; y++ {
				view.SetBlock(wx, y, wz, stone)
			}
			for y := floorY + 1; y <= waterTop; y++ {
				view.SetBlock(wx, y, wz, water)
			}
		}
	}
	// A packed_ice block adjacent (east) to the origin at floor+1 so the neighbour scan finds it.
	view.SetBlock(1, floorY+1, 0, packed)
	return view
}

// TestBlueIcePlacesOnFloor: with water at the origin and a packed_ice neighbour, blue_ice's base
// block lands at the origin (BlueIceFeature.place).
func TestBlueIcePlacesOnFloor(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop, seaLevel = 40, 62, 63
	blueIce := block.ToStateID[block.BlueIce{}]

	view := buildOceanFloor(minY, height, floorY, waterTop)
	// origin at the water column just above the floor.
	origin := placement.BlockPos{X: 0, Y: floorY + 1, Z: 0}
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
	bctx := &bodyContext{view: view, seaLevel: seaLevel}

	ok := blueIceBody(bctx, blueIceCF(), ctx, levelgen.NewLegacyRandomSource(7), origin)
	if !ok {
		t.Fatalf("blueIceBody returned false; expected placement")
	}
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != blueIce {
		t.Fatalf("origin block is %d, want BLUE_ICE %d", got, blueIce)
	}
}

// TestBlueIceGateRejectsAboveSea: an origin above sea level - 1 is rejected (no placement, no draws).
func TestBlueIceGateRejectsAboveSea(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop, seaLevel = 40, 70, 63
	view := buildOceanFloor(minY, height, floorY, waterTop)
	origin := placement.BlockPos{X: 0, Y: 64, Z: 0} // > seaLevel-1 == 62
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
	bctx := &bodyContext{view: view, seaLevel: seaLevel}
	if blueIceBody(bctx, blueIceCF(), ctx, levelgen.NewLegacyRandomSource(7), origin) {
		t.Fatalf("blueIceBody accepted an origin above sea level")
	}
}

// TestBlueIceDeterministic: identical seed -> identical placement over the whole view.
func TestBlueIceDeterministic(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop, seaLevel = 40, 62, 63
	run := func() *Neighborhood {
		view := buildOceanFloor(minY, height, floorY, waterTop)
		origin := placement.BlockPos{X: 0, Y: floorY + 1, Z: 0}
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
		bctx := &bodyContext{view: view, seaLevel: seaLevel}
		blueIceBody(bctx, blueIceCF(), ctx, levelgen.NewLegacyRandomSource(42), origin)
		return view
	}
	v1, v2 := run(), run()
	for wx := -16; wx <= 31; wx++ {
		for wz := -16; wz <= 31; wz++ {
			for y := floorY; y <= waterTop; y++ {
				if v1.GetBlock(wx, y, wz) != v2.GetBlock(wx, y, wz) {
					t.Fatalf("blue_ice not deterministic at (%d,%d,%d)", wx, y, wz)
				}
			}
		}
	}
}
