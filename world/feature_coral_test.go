package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// fillWaterColumn fills the whole 3x3 view (all sections) with water so the coral bodies have a
// water medium to grow through (CoralFeature.placeCoralBlock requires pos+above to be water).
func fillWaterColumn(view *Neighborhood, minY, height int) {
	water := block.ToStateID[block.Water{Level: 0}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := dx*16, dz*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y < minY+height; y++ {
						view.SetBlock(bx+lx, y, bz+lz, water)
					}
				}
			}
		}
	}
}

// coralBlockCount counts placed coral BLOCK states (the 5 CORAL_BLOCKS) in the reach box.
func coralBlockCount(view *Neighborhood, origin placement.BlockPos, reach, yLo, yHi int) int {
	set := map[block.StateID]bool{}
	for _, id := range coralBlockFamilies {
		set[id] = true
	}
	count := 0
	for y := origin.Y + yLo; y <= origin.Y+yHi; y++ {
		for z := origin.Z - reach; z <= origin.Z+reach; z++ {
			for x := origin.X - reach; x <= origin.X+reach; x++ {
				if set[view.GetBlock(x, y, z)] {
					count++
				}
			}
		}
	}
	return count
}

// TestCoralFamilyPickDeterministic proves coralPickBlockFamily maps a seed's first nextInt(5)
// draw to a fixed CORAL_BLOCKS family, matching the tag order.
func TestCoralFamilyPickDeterministic(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 0xC0FFEE, 0x5EED} {
		rng := levelgen.NewWorldgenRandom(seed)
		oracle := levelgen.NewWorldgenRandom(seed)
		got := coralPickBlockFamily(rng)
		want := coralBlockFamilies[int(oracle.NextIntN(5))]
		if got != want {
			t.Fatalf("seed %d: family pick %d != oracle %d", seed, got, want)
		}
	}
}

// TestPlaceCoralBlockPlacesAndRejects proves placeCoralBlock places the coral block in water and
// returns false (placing nothing) when the position is not water/coral.
func TestPlaceCoralBlockPlacesAndRejects(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 62, Z: 8}
	family := coralBlockFamilies[0] // tube_coral_block

	// Reject: solid (non-water, non-coral) at pos.
	view := build3x3([2]int{0, 0}, minY, height)
	view.SetBlock(origin.X, origin.Y, origin.Z, block.ToStateID[block.Stone{}])
	view.SetBlock(origin.X, origin.Y+1, origin.Z, block.ToStateID[block.Water{Level: 0}])
	if placeCoralBlock(&bodyContext{view: view}, levelgen.NewWorldgenRandom(1), origin, family) {
		t.Fatalf("placeCoralBlock should reject a non-water/non-coral position")
	}

	// Accept: water at pos and above -> the coral block is placed.
	view2 := build3x3([2]int{0, 0}, minY, height)
	fillWaterColumn(view2, minY, height)
	if !placeCoralBlock(&bodyContext{view: view2}, levelgen.NewWorldgenRandom(1), origin, family) {
		t.Fatalf("placeCoralBlock should accept a water position")
	}
	if got := view2.GetBlock(origin.X, origin.Y, origin.Z); got != family {
		t.Fatalf("coral block not placed: got %d want %d", got, family)
	}
}

// TestPlaceCoralBlockDrawOrder proves placeCoralBlock consumes rng in the exact javap order:
// the coral block is placed (no draw), then nextFloat() for the on-top decoration branch. When
// nextFloat()>=0.25 and the second nextFloat()>=0.05 there is NO on-top block; then 4 iterations
// of nextFloat() for the wall-fan gate (each < 0.2 also draws a wall-fan family nextInt(5)).
func TestPlaceCoralBlockDrawOrder(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 62, Z: 8}
	family := coralBlockFamilies[0]

	view := build3x3([2]int{0, 0}, minY, height)
	fillWaterColumn(view, minY, height)
	rng := levelgen.NewWorldgenRandom(0x0C0A17)
	placeCoralBlock(&bodyContext{view: view}, rng, origin, family)

	// Oracle replays the SAME draw sequence.
	oracle := levelgen.NewWorldgenRandom(0x0C0A17)
	// on-top branch:
	if oracle.NextFloat() < 0.25 {
		_ = oracle.NextIntN(10) // fan pick
	} else if oracle.NextFloat() < 0.05 {
		_ = oracle.NextIntN(4) // sea pickle pickles
	}
	// 4 horizontal wall-fan gates:
	for i := 0; i < 4; i++ {
		if oracle.NextFloat() < 0.2 {
			_ = oracle.NextIntN(5) // wall-fan family (neighbour is water in a filled column)
		}
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("placeCoralBlock draw sequence diverged from the javap oracle")
	}
}

// TestCoralBodiesDeterministic proves each coral body is deterministic (same seed -> identical
// view) and actually places at least one coral block in a water medium.
func TestCoralBodiesDeterministic(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 50, Z: 8}

	bodies := map[string]featureBody{
		"coral_tree":     coralTreeBody,
		"coral_claw":     coralClawBody,
		"coral_mushroom": coralMushroomBody,
	}
	for name, body := range bodies {
		build := func() *Neighborhood {
			v := build3x3([2]int{0, 0}, minY, height)
			fillWaterColumn(v, minY, height)
			return v
		}
		const seed = int64(0x2EA1)
		a := build()
		b := build()
		ra := levelgen.NewWorldgenRandom(seed)
		rb := levelgen.NewWorldgenRandom(seed)
		ctx := newPlacementContext(a, minY, height, nil)
		if !body(&bodyContext{view: a}, nil, ctx, ra, origin) {
			t.Fatalf("%s returned false", name)
		}
		ctxB := newPlacementContext(b, minY, height, nil)
		if !body(&bodyContext{view: b}, nil, ctxB, rb, origin) {
			t.Fatalf("%s (run2) returned false", name)
		}
		assertNetherViewsEqual(t, a, b, origin, 12, -8, 24)
		if coralBlockCount(a, origin, 12, -8, 24) == 0 {
			t.Fatalf("%s placed no coral blocks in a water medium", name)
		}
	}
}
