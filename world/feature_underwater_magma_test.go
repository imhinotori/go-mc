package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// underwaterMagmaCF builds the underwater_magma configured feature with the vanilla
// underwater_magma.json values (floor_search_range=5, radius=1, probability given).
func underwaterMagmaCF(prob string) *feature.ConfiguredFeature {
	raw := []byte(`{"floor_search_range":5,"placement_radius_around_floor":1,"placement_probability_per_valid_position":` + prob + `}`)
	return &feature.ConfiguredFeature{Type: "underwater_magma", Config: &feature.ParsedConfig{Raw: raw}}
}

// buildMagmaOcean fills the 3x3 with a solid stone floor topped by water. The origin sits in the
// water; the floor is at floorY (stone), water fills floorY+1..waterTop.
func buildMagmaOcean(minY, height, floorY, waterTop int) *Neighborhood {
	view := build3x3([2]int{0, 0}, minY, height)
	stone := block.ToStateID[block.Stone{}]
	water := block.ToStateID[block.Water{Level: 0}]
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
	return view
}

// TestUnderwaterMagmaPlacesMagma: with probability 1.0, a valid enclosed floor block under water
// becomes magma_block. The floor cell directly under the origin column is fully enclosed (stone
// on all sides), so it is a valid placement.
func TestUnderwaterMagmaPlacesMagma(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop = 40, 62
	magma := block.ToStateID[block.MagmaBlock{}]

	view := buildMagmaOcean(minY, height, floorY, waterTop)
	origin := placement.BlockPos{X: 0, Y: floorY + 3, Z: 0} // in the water column above the floor
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
	bctx := &bodyContext{view: view, seaLevel: 63}

	ok := underwaterMagmaBody(bctx, underwaterMagmaCF("1.0"), ctx, levelgen.NewLegacyRandomSource(5), origin)
	if !ok {
		t.Fatalf("underwaterMagmaBody returned false; expected placement with prob=1.0")
	}
	// At least one magma_block should exist within the radius-1 cube around the floor.
	count := 0
	for wx := -1; wx <= 1; wx++ {
		for wz := -1; wz <= 1; wz++ {
			for y := floorY - 1; y <= floorY+1; y++ {
				if view.GetBlock(wx, y, wz) == magma {
					count++
				}
			}
		}
	}
	if count == 0 {
		t.Fatalf("no magma_block placed around the ocean floor")
	}
}

// TestUnderwaterMagmaNoWaterAtOrigin: an origin not in water yields no floor (getFloorY empty) ->
// no placement.
func TestUnderwaterMagmaNoWaterAtOrigin(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop = 40, 62
	view := buildMagmaOcean(minY, height, floorY, waterTop)
	origin := placement.BlockPos{X: 0, Y: floorY, Z: 0} // inside the stone floor, not water
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
	bctx := &bodyContext{view: view, seaLevel: 63}
	if underwaterMagmaBody(bctx, underwaterMagmaCF("1.0"), ctx, levelgen.NewLegacyRandomSource(5), origin) {
		t.Fatalf("underwaterMagmaBody accepted a non-water origin")
	}
}

// TestUnderwaterMagmaDeterministic: identical seed -> identical placement (nextFloat() per box
// cell in betweenClosed order is the determinism contract).
func TestUnderwaterMagmaDeterministic(t *testing.T) {
	const minY, height = -64, 384
	const floorY, waterTop = 40, 62
	run := func() *Neighborhood {
		view := buildMagmaOcean(minY, height, floorY, waterTop)
		origin := placement.BlockPos{X: 0, Y: floorY + 3, Z: 0}
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type { return biome.Type(0) })
		bctx := &bodyContext{view: view, seaLevel: 63}
		underwaterMagmaBody(bctx, underwaterMagmaCF("0.5"), ctx, levelgen.NewLegacyRandomSource(99), origin)
		return view
	}
	v1, v2 := run(), run()
	for wx := -2; wx <= 2; wx++ {
		for wz := -2; wz <= 2; wz++ {
			for y := floorY - 2; y <= floorY+2; y++ {
				if v1.GetBlock(wx, y, wz) != v2.GetBlock(wx, y, wz) {
					t.Fatalf("underwater_magma not deterministic at (%d,%d,%d)", wx, y, wz)
				}
			}
		}
	}
}
