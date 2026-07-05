package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_geode_test.go pins the GeodeFeature body (GeodeFeature.place, CFR/javap -c against
// 26.2-inner.jar): a geode placed inside solid stone carves its layered shell
// (smooth_basalt outer / calcite middle / amethyst_block inner / budding_amethyst alternate)
// and an inner air pocket. The determinism contract: the body is pure over (rng, view, world
// seed) — a re-run on a seed-matched view (same world seed → same NormalNoise) is bit-identical.

// fillSolidStoneAll fills the entire 3x3 with stone over [loY, hiY], giving the geode a solid
// medium so its anchor points never land in air (invalid) and every shell cell is replaceable.
func fillSolidStoneAll(view *Neighborhood, center [2]int, loY, hiY int) {
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := loY; y <= hiY; y++ {
						view.SetBlock(bx+lx, y, bz+lz, stone)
					}
				}
			}
		}
	}
}

func TestGeodePlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:amethyst_geode")
	if err != nil {
		t.Fatalf("ResolveConfigured(amethyst_geode): %v", err)
	}
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	const worldSeed = int64(0x9EE0_D3_5EED)

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		// Solid stone spanning well beyond the geode's ±16 gen offset around the origin.
		fillSolidStoneAll(view, center, origin.Y-20, origin.Y+20)
		view.SetWorldSeed(worldSeed)
		return view
	}

	// A seed that produces a geode (geodes place unconditionally once anchors land in stone;
	// the whole medium is stone here so it never aborts).
	const seed = int64(0xA3E7_0DE)
	view := build()
	bctx := &bodyContext{view: view, reg: reg}
	ok := geodeBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	if !ok {
		t.Fatalf("geode body returned false in a solid-stone medium (expected placement)")
	}

	// The geode must place its calcite middle layer + at least one amethyst-family block.
	calcite := countGeodeStates(view, origin, "minecraft:calcite")
	smoothBasalt := countGeodeStates(view, origin, "minecraft:smooth_basalt")
	amethyst := countGeodeStates(view, origin, "minecraft:amethyst_block")
	budding := countGeodeStates(view, origin, "minecraft:budding_amethyst")
	if calcite == 0 {
		t.Fatalf("geode placed no calcite (middle layer)")
	}
	if smoothBasalt == 0 {
		t.Fatalf("geode placed no smooth_basalt (outer layer)")
	}
	if amethyst == 0 && budding == 0 {
		t.Fatalf("geode placed no amethyst_block / budding_amethyst (inner layer)")
	}

	// Determinism: same world seed (same NormalNoise) + same feature rng → bit-identical.
	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: reg}
	geodeBody(bctx2, cf, newPlacementContext(view2, minY, height, nil), levelgen.NewWorldgenRandom(seed), origin)
	assertCaveViewsEqual(t, view, view2, origin, 18, origin.Y-18, origin.Y+18)
}

func TestGeodeAbortsInAir(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:amethyst_geode")
	if err != nil {
		t.Fatalf("ResolveConfigured(amethyst_geode): %v", err)
	}
	origin := placement.BlockPos{X: 8, Y: 40, Z: 8}
	// Empty view (all air): every anchor point lands in air → numInvalidPoints exceeds the
	// invalid_blocks_threshold (1) → place returns false with no blocks.
	view := build3x3(center, minY, height)
	view.SetWorldSeed(1)
	bctx := &bodyContext{view: view, reg: reg}
	if geodeBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(7), origin) {
		t.Fatalf("geode placed in an all-air medium (expected abort on invalid anchors)")
	}
	if countGeodeStates(view, origin, "minecraft:calcite") != 0 {
		t.Fatalf("geode wrote blocks despite aborting")
	}
}

// countGeodeStates counts cells over the ±18 gen box around origin holding a state of `want`.
func countGeodeStates(view *Neighborhood, origin placement.BlockPos, want string) int {
	n := 0
	for dx := -18; dx <= 18; dx++ {
		for dz := -18; dz <= 18; dz++ {
			for dy := -18; dy <= 18; dy++ {
				st := view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == want {
					n++
				}
			}
		}
	}
	return n
}
