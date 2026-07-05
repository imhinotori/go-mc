package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// feature_scattered_ore_test.go pins the LIVE "scattered_ore" body (ScatteredOreFeature.place,
// javap -c, 26.2-inner.jar): a scatter of ancient_debris into matching nether base stone,
// deterministic over a seed-matched view, with the post-place rng fingerprint reproducible.

// fillNetherrack fills the 3x3 with netherrack from minY..topY (the #base_stone_nether target
// the ancient-debris scattered ore replaces).
func fillNetherrack(view *Neighborhood, center [2]int, minY, topY int) {
	nr := block.ToStateID[block.FromID["minecraft:netherrack"]]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y <= topY; y++ {
						view.SetBlock(bx+lx, y, bz+lz, nr)
					}
				}
			}
		}
	}
}

func resolveCF(t *testing.T, id string) *feature.ConfiguredFeature {
	t.Helper()
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured(id)
	if err != nil {
		t.Fatalf("ResolveConfigured(%s): %v", id, err)
	}
	return cf
}

func TestScatteredOrePlaces(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	cf := resolveCF(t, "minecraft:ore_ancient_debris_large")
	origin := placement.BlockPos{X: 8, Y: 20, Z: 8}
	debris := block.ToStateID[block.FromID["minecraft:ancient_debris"]]

	build := func() *Neighborhood {
		view := build3x3(center, minY, height)
		fillNetherrack(view, center, minY, origin.Y+16)
		return view
	}

	const seed = int64(0x5CA77E20)
	// ore_ancient_debris_large has discard_chance_on_air_exposure=1.0, so buried debris (no
	// air neighbors, all netherrack) is ALWAYS kept when the target matches. Run several
	// seeds to guarantee at least one placement (nextInt(size+1) can roll 0).
	placedAny := false
	for s := int64(0); s < 40 && !placedAny; s++ {
		view := build()
		bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
		rng := levelgen.NewWorldgenRandom(seed + s)
		scatteredOreBody(bctx, cf, nil, rng, origin)
		for dx := -8; dx <= 8; dx++ {
			for dy := -8; dy <= 8; dy++ {
				for dz := -8; dz <= 8; dz++ {
					if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) == debris {
						placedAny = true
					}
				}
			}
		}
	}
	if !placedAny {
		t.Fatalf("scattered_ore never placed ancient_debris into netherrack across 40 seeds")
	}

	// Determinism: a fresh seed-matched run is bit-identical + the post-place rng matches.
	view := build()
	bctx := &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}
	rng := levelgen.NewWorldgenRandom(seed)
	scatteredOreBody(bctx, cf, nil, rng, origin)

	view2 := build()
	bctx2 := &bodyContext{view: view2, reg: feature.NewEmbeddedRegistry()}
	rng2 := levelgen.NewWorldgenRandom(seed)
	scatteredOreBody(bctx2, cf, nil, rng2, origin)

	for dx := -8; dx <= 8; dx++ {
		for dy := -8; dy <= 8; dy++ {
			for dz := -8; dz <= 8; dz++ {
				a := view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				b := view2.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if a != b {
					t.Fatalf("non-deterministic scattered_ore at (%d,%d,%d): %v vs %v", dx, dy, dz, a, b)
				}
			}
		}
	}
	if rng.NextLong() != rng2.NextLong() {
		t.Fatalf("post-place rng fingerprint diverged")
	}
}

// TestJavaRoundF pins the Math.round(float) port against known Java results (half-up ties).
func TestJavaRoundF(t *testing.T) {
	cases := []struct {
		in   float32
		want int
	}{
		{0.0, 0}, {0.5, 1}, {-0.5, 0}, {1.5, 2}, {2.5, 3}, {-1.5, -1},
		{0.4, 0}, {0.6, 1}, {-0.4, 0}, {-0.6, -1}, {3.49, 3}, {3.5, 4},
	}
	for _, c := range cases {
		if got := javaRoundF(c.in); got != c.want {
			t.Errorf("javaRoundF(%v) = %d; want %d", c.in, got, c.want)
		}
	}
}
