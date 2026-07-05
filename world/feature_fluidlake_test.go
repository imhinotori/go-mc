package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// fillStoneBox fills the 3x3's Y range around the lake box with stone so the lake's wall
// integrity + canPlaceFeature validation passes (empty air walls abort the lake).
func fillStoneBox(view *Neighborhood, center [2]int, y0, y1 int) {
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			baseX := (center[0] + dx) * 16
			baseZ := (center[1] + dz) * 16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := y0; y <= y1; y++ {
						view.SetBlock(baseX+lx, y, baseZ+lz, stone)
					}
				}
			}
		}
	}
}

// lakeLavaCount counts lava cells in the lake box for footprint comparison.
func lakeLavaCount(view *Neighborhood, origin placement.BlockPos) int {
	lava := block.ToStateID[block.Lava{Level: 0}]
	n := 0
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			for k := 0; k < 8; k++ {
				if view.GetBlock(origin.X-8+i, origin.Y-4+k, origin.Z-8+j) == lava {
					n++
				}
			}
		}
	}
	return n
}

// TestLakePlacesLavaInSolidTerrain: a lava lake carved into a solid-stone region passes wall
// validation and fills fluid below the lake line (a positive placement).
func TestLakePlacesLavaInSolidTerrain(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	// The lake box is origin.offset(-8,-4,-8) .. +16 in X/Z, +8 in Y. Fill stone across the
	// whole Y span the box touches (origin.Y-4 .. origin.Y+4).
	origin := placement.BlockPos{X: 8, Y: 50, Z: 8}
	fillStoneBox(view, center, origin.Y-6, origin.Y+6)

	cf := cfFromEmbedded(t, "lake_lava")
	bctx := &bodyContext{view: view, seaLevel: 63}
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type {
		bt := biomeType(t, "minecraft:plains")
		return bt
	})

	if !lakeBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(0x1A4E), origin) {
		t.Fatalf("lakeBody returned false; want a lava lake in solid terrain")
	}
	if n := lakeLavaCount(view, origin); n == 0 {
		t.Fatalf("lake placed no lava; want a positive fluid footprint")
	}
}

// TestLakeDeterministic: same seed + same solid terrain -> identical lava footprint (the shape
// draws + barrier draws are a fixed sequence for a given rng).
func TestLakeDeterministic(t *testing.T) {
	const minY, height = -64, 384
	run := func(seed int64) int {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		origin := placement.BlockPos{X: 8, Y: 50, Z: 8}
		fillStoneBox(view, center, origin.Y-6, origin.Y+6)
		cf := cfFromEmbedded(t, "lake_lava")
		ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type {
			return biomeType(t, "minecraft:plains")
		})
		lakeBody(&bodyContext{view: view, seaLevel: 63}, cf, ctx, levelgen.NewLegacyRandomSource(seed), origin)
		return lakeLavaCount(view, origin)
	}
	a, b := run(0x2B5F), run(0x2B5F)
	if a != b {
		t.Fatalf("lake not deterministic: %d vs %d lava cells", a, b)
	}
	if a == 0 {
		t.Fatalf("lake placed no lava; expected positive footprint")
	}
}

// TestLakeAbortsInEmptyTerrain: a lake over empty air aborts (air walls fail the k<4 solid
// check) — it returns false and places nothing, but MUST still consume the shape draws so the
// post-lake rng stream matches a run that abandons after the shape phase.
func TestLakeAbortsInEmptyTerrain(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height) // all air
	origin := placement.BlockPos{X: 8, Y: 50, Z: 8}
	cf := cfFromEmbedded(t, "lake_lava")
	ctx := newPlacementContext(view, minY, height, func(x, y, z int) biome.Type {
		return biomeType(t, "minecraft:plains")
	})

	rng := levelgen.NewLegacyRandomSource(0x77)
	if lakeBody(&bodyContext{view: view, seaLevel: 63}, cf, ctx, rng, origin) {
		t.Fatalf("lake placed in empty terrain; want abort")
	}

	// The shape draws must have been consumed: an independent oracle drawing count=nextInt(4)+4
	// followed by count*6 nextDouble should leave the rng at the SAME state (validation + carve
	// take 0 draws; the barrier phase never runs because validation aborted).
	oracle := levelgen.NewLegacyRandomSource(0x77)
	count := int(oracle.NextIntN(4)) + 4
	for i := 0; i < count; i++ {
		for d := 0; d < 6; d++ {
			oracle.NextDouble()
		}
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("lake abort did not consume exactly the shape draws (count*6 nextDouble + 1 nextInt)")
	}
}
