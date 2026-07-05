package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_desert_well_test.go pins the LIVE "desert_well" body (DesertWellFeature.place,
// javap -c, 26.2-inner.jar): on a solid sand surface it carves the sandstone bowl + water +
// slabs + pillars pattern and buries two suspicious-sand blocks. It is a fixed pattern
// (zero placement draws); the only draws are the two suspicious-sand nextInt(5) picks — so
// the whole placement is deterministic and the post-place rng fingerprint is reproducible.

// fillSand fills the 3x3 with sand from minY..topY (the surface DesertWellFeature requires).
func fillSand(view *Neighborhood, center [2]int, minY, topY int) {
	sand := block.ToStateID[block.FromID["minecraft:sand"]]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= topY; y++ {
						view.SetBlock(bx+lx, y, bz+lz, sand)
					}
				}
			}
		}
	}
}

func TestDesertWellPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:desert_well")
	// origin sits ON the sand surface top; the body starts at origin.above() then descends to
	// the first sand block (the surface top). Keep well inside the 3x3.
	surfaceTop := 40
	origin := placement.BlockPos{X: 8, Y: surfaceTop, Z: 8}

	sandstone := block.ToStateID[block.FromID["minecraft:sandstone"]]
	water := block.ToStateID[block.FromID["minecraft:water"]]
	susSand := block.ToStateID[block.FromID["minecraft:suspicious_sand"]]

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// Fill sand up to the surface top; air above it. The body descends from origin.above()
		// (air) to origin.Y (= surfaceTop, sand).
		fillSand(view, center, minY, surfaceTop)
		return view
	}

	const seed = int64(0xDE5E27)
	view := build()
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	rng := levelgen.NewWorldgenRandom(seed)
	if !desertWellBody(bctx, cf, ctx, rng, origin) {
		t.Fatalf("desert_well rejected a valid solid sand surface")
	}

	// pos (the descended surface top) becomes water; the bowl below/around is sandstone.
	if view.GetBlock(origin.X, origin.Y, origin.Z) != water {
		t.Fatalf("well center is not water: %v", view.GetBlock(origin.X, origin.Y, origin.Z))
	}
	// A sandstone bowl cell (offset x=2,y=-2,z=0 from pos) exists.
	if view.GetBlock(origin.X+2, origin.Y-2, origin.Z) != sandstone {
		t.Fatalf("no sandstone bowl at (+2,-2,0)")
	}
	// The center pillar cap at y+4 is sandstone.
	if view.GetBlock(origin.X, origin.Y+4, origin.Z) != sandstone {
		t.Fatalf("no sandstone cap at (0,+4,0)")
	}
	// Two suspicious-sand blocks were buried under the floor (one at -1, one at -2 of a picked
	// cardinal). Count them across the footprint.
	susCount := 0
	for dx := -2; dx <= 2; dx++ {
		for dy := -3; dy <= 0; dy++ {
			for dz := -2; dz <= 2; dz++ {
				if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) == susSand {
					susCount++
				}
			}
		}
	}
	if susCount == 0 {
		t.Fatalf("no suspicious_sand buried (the two Util.getRandom picks missing?)")
	}

	// Determinism: a fresh seed-matched run is bit-identical + the post-place rng matches.
	view2 := build()
	bctx2 := &bodyContext{view: view2}
	ctx2 := newPlacementContext(view2, minY, height, nil)
	rng2 := levelgen.NewWorldgenRandom(seed)
	desertWellBody(bctx2, cf, ctx2, rng2, origin)
	for dx := -3; dx <= 3; dx++ {
		for dy := -3; dy <= 5; dy++ {
			for dz := -3; dz <= 3; dz++ {
				a := view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				b := view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if a != b {
					t.Fatalf("non-deterministic desert_well at (%d,%d,%d): %v vs %v", dx, dy, dz, a, b)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged")
	}
}

// TestDesertWellRejectsNonSand: a non-sand surface (stone) is rejected with no placement.
func TestDesertWellRejectsNonSand(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:desert_well")
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}

	view := build3x3(center, minY, height)
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= origin.Y; y++ {
						view.SetBlock(bx+lx, y, bz+lz, stone)
					}
				}
			}
		}
	}
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	rng := levelgen.NewWorldgenRandom(1)
	if desertWellBody(bctx, cf, ctx, rng, origin) {
		t.Fatalf("desert_well accepted a non-sand (stone) surface")
	}
}
