package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_fossil_test.go pins the LIVE "fossil" body (FossilFeature.place, javap -c,
// 26.2-inner.jar): it picks + rotates a spine/skull template, drops it under the OCEAN_FLOOR_WG
// height, and places bone_block (+ the coal overlay ore) through the block_rot / protected_blocks
// processor chain. Determinism + positive placement (bone_block appears in solid stone).

// fossilHeightCtx wraps placementContext to pin OCEAN_FLOOR_WG at a fixed floor so the fossil's
// drop depth is controllable in the synthetic chunk (mirrors feature_ore_test's oreColumnContext).
type fossilHeightCtx struct {
	*placementContext
	floor int
}

func (c fossilHeightCtx) GetHeight(_ placement.HeightmapType, _, _ int) int { return c.floor }

func fillStoneAll(view *Neighborhood, center [2]int, minY, topY int) {
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= topY; y++ {
						view.SetBlock(bx+lx, y, bz+lz, stone)
					}
				}
			}
		}
	}
}

func TestFossilPlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:fossil_coal")
	origin := placement.BlockPos{X: 8, Y: 60, Z: 8}
	bone := block.ToStateID[block.FromID["minecraft:bone_block"]]

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// Solid stone up through the surface so the buried fossil sits in stone (corners not
		// air -> passes countEmptyCorners; block_rot places bone into stone).
		fillStoneAll(view, center, minY, 80)
		return view
	}
	mkCtx := func(view *Neighborhood) placement.PlacementContext {
		return fossilHeightCtx{placementContext: newPlacementContext(view, minY, height, nil), floor: 70}
	}

	// The fossil's placement depends on nextInt(8) (template), nextInt(4) (rotation), nextInt(10)
	// (depth), then per-block block_rot rolls. Sweep seeds to guarantee visible bone placement.
	const seed = int64(0xF0551700)
	placedAny := false
	for s := int64(0); s < 30 && !placedAny; s++ {
		view := build()
		bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
		rng := levelgen.NewWorldgenRandom(seed + s)
		if !fossilBody(bctx, cf, mkCtx(view), rng, origin) {
			continue // a too-exposed drop is rejected; try another seed
		}
		for dx := -12; dx <= 12; dx++ {
			for dy := -20; dy <= 4; dy++ {
				for dz := -12; dz <= 12; dz++ {
					if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) == bone {
						placedAny = true
					}
				}
			}
		}
	}
	if !placedAny {
		t.Fatalf("fossil never placed bone_block across 30 seeds")
	}

	// Determinism: a fresh seed-matched run is bit-identical + the post-place rng matches.
	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	rng := levelgen.NewWorldgenRandom(seed)
	fossilBody(bctx, cf, mkCtx(view), rng, origin)
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	rng2 := levelgen.NewWorldgenRandom(seed)
	fossilBody(bctx2, cf, mkCtx(view2), rng2, origin)
	for dx := -14; dx <= 14; dx++ {
		for dy := -24; dy <= 6; dy++ {
			for dz := -14; dz <= 14; dz++ {
				if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) != view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) {
					t.Fatalf("non-deterministic fossil at (%d,%d,%d)", dx, dy, dz)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged")
	}
}
