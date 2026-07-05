package world

import (
	"fmt"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// These tests pin the vegetation feature bodies' DRAW ORDER (the determinism contract) and
// their block placement for known seeds. The aquatic bodies (seagrass/kelp/sea_pickle) need
// a sea-floor column: a solid block at floorY (raises OCEAN_FLOOR_WG to floorY+1) with water
// above, so getHeight(OCEAN_FLOOR) lands on the water cell just above the floor.

// vegBctx builds a bodyContext over a fresh 3x3 (no floor).
func vegBctx() (*bodyContext, *Neighborhood) {
	view := build3x3([2]int{0, 0}, -64, 384)
	return &bodyContext{view: view, reg: feature.NewEmbeddedRegistry()}, view
}

// fillSeaColumn lays a solid floor at floorY across the whole 3x3 and floods `waterDepth`
// water cells above it (floorY+1 .. floorY+waterDepth). This makes getHeight(OCEAN_FLOOR)
// return floorY+1 (water does not count as ocean floor), and the placement cell is water.
func fillSeaColumn(view *Neighborhood, floorY, waterDepth int) {
	stone := block.ToStateID[block.Stone{}]
	water := block.ToStateID[block.Water{Level: 0}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := dx*16, dz*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					view.SetBlock(bx+lx, floorY, bz+lz, stone)
					for wy := 1; wy <= waterDepth; wy++ {
						view.SetBlock(bx+lx, floorY+wy, bz+lz, water)
					}
				}
			}
		}
	}
}

// fillLandFloor lays a solid dirt floor at floorY (air above), for bamboo/vines.
func fillLandFloor(view *Neighborhood, floorY int) {
	dirt := block.ToStateID[block.Dirt{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := dx*16, dz*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					view.SetBlock(bx+lx, floorY, bz+lz, dirt)
				}
			}
		}
	}
}

func vegCF(t *testing.T, ftype, config string) *feature.ConfiguredFeature {
	reg := feature.NewEmbeddedRegistry()
	obj := fmt.Sprintf(`{"type":"minecraft:%s","config":%s}`, ftype, config)
	cf, err := reg.ParseConfiguredFeature("minecraft:test_"+ftype, []byte(obj))
	if err != nil {
		t.Fatalf("ParseConfiguredFeature(%s): %v", ftype, err)
	}
	return cf
}

// ---- seagrass ----

// TestSeagrassPlacement: seagrass on a water-over-stone column places at the ocean floor
// cell, and the draw order (x, z, then nextDouble()) matches a parallel oracle.
func TestSeagrassPlacement(t *testing.T) {
	const floorY = 40
	bctx, view := vegBctx()
	fillSeaColumn(view, floorY, 12) // water floorY+1..floorY+12
	origin := placement.BlockPos{X: 4, Y: floorY + 6, Z: 4}

	// probability 1.0 -> always tall (nextDouble()<1.0 is always true), so a lower+upper
	// pair lands (both cells are water).
	cf := vegCF(t, "seagrass", `{"probability":1.0}`)

	const seed = int64(0x5EA9)
	rng := levelgen.NewWorldgenRandom(seed)
	if !seagrassBody(bctx, cf, nil, rng, origin) {
		t.Fatalf("seagrass returned false over a water column")
	}

	// Oracle: x, z draws, then y from the (fixed) ocean-floor heightmap = floorY+1.
	oracle := levelgen.NewWorldgenRandom(seed)
	x := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
	z := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
	_ = oracle.NextDouble() // isTall draw (inside the water branch)
	gx, gz := origin.X+x, origin.Z+z
	gy := floorY + 1

	lower := block.ToStateID[block.TallSeagrass{Half: block.DoubleBlockHalfLower}]
	upper := block.ToStateID[block.TallSeagrass{Half: block.DoubleBlockHalfUpper}]
	if got := view.GetBlock(gx, gy, gz); got != lower {
		t.Fatalf("seagrass lower not placed at ocean floor cell (%d,%d,%d): got %d want %d", gx, gy, gz, got, lower)
	}
	if got := view.GetBlock(gx, gy+1, gz); got != upper {
		t.Fatalf("tall seagrass upper not placed above lower: got %d want %d", got, upper)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("seagrass draw sequence diverged from the jar-order oracle")
	}
}

// TestSeagrassShort: probability 0.0 -> never tall, a single SEAGRASS block lands.
func TestSeagrassShort(t *testing.T) {
	const floorY = 30
	bctx, view := vegBctx()
	fillSeaColumn(view, floorY, 8)
	origin := placement.BlockPos{X: 8, Y: floorY + 4, Z: 8}
	cf := vegCF(t, "seagrass", `{"probability":0.0}`)

	const seed = int64(0x11)
	rng := levelgen.NewWorldgenRandom(seed)
	seagrassBody(bctx, cf, nil, rng, origin)

	oracle := levelgen.NewWorldgenRandom(seed)
	x := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
	z := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
	gx, gz := origin.X+x, origin.Z+z
	seagrass := block.DefaultStateID["minecraft:seagrass"]
	if got := view.GetBlock(gx, floorY+1, gz); got != seagrass {
		t.Fatalf("short seagrass not placed: got %d want %d", got, seagrass)
	}
}

// ---- kelp ----

// TestKelpColumn: kelp builds a vertical column of kelp_plant topped by a kelp head, of
// height 1+nextInt(10). Assert the column height matches the drawn height and the top cell
// is a KELP head.
func TestKelpColumn(t *testing.T) {
	const floorY = 20
	bctx, view := vegBctx()
	fillSeaColumn(view, floorY, 40) // deep water so the whole column fits
	origin := placement.BlockPos{X: 2, Y: floorY + 20, Z: 2}

	rng := levelgen.NewWorldgenRandom(0xCE10)
	if !kelpBody(bctx, nil, nil, rng, origin) {
		t.Fatalf("kelp returned false over a deep water column")
	}

	// Oracle for the height draw (first draw).
	oracle := levelgen.NewWorldgenRandom(0xCE10)
	height := 1 + int(oracle.NextIntN(10))

	kelpPlant := block.ToStateID[block.KelpPlant{}]
	base := floorY + 1
	// Bodies 0..height-1 are kelp_plant; index height is a KELP head.
	plantCount := 0
	for h := 0; h < height; h++ {
		if got := view.GetBlock(origin.X, base+h, origin.Z); got == kelpPlant {
			plantCount++
		}
	}
	if plantCount == 0 {
		t.Fatalf("kelp placed no plant body blocks (height=%d)", height)
	}
	// The head is a KELP block at base+height (age in [20,23]).
	head := view.GetBlock(origin.X, base+height, origin.Z)
	if !isKelpBlock(head) {
		t.Fatalf("kelp head (KELP block) not at column top base+height=%d: got %d", base+height, head)
	}
}

// TestKelpDeterministic: same seed -> same column twice (pure function of rng+view).
func TestKelpDeterministic(t *testing.T) {
	run := func() ([]block.StateID, int64) {
		bctx, view := vegBctx()
		fillSeaColumn(view, 20, 40)
		origin := placement.BlockPos{X: 2, Y: 40, Z: 2}
		rng := levelgen.NewWorldgenRandom(999)
		kelpBody(bctx, nil, nil, rng, origin)
		var snap []block.StateID
		for y := 20; y <= 62; y++ {
			snap = append(snap, view.GetBlock(2, y, 2))
		}
		return snap, rng.NextLong()
	}
	s1, f1 := run()
	s2, f2 := run()
	if f1 != f2 {
		t.Fatalf("kelp rng fingerprint differs: %d vs %d", f1, f2)
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("kelp non-deterministic at index %d", i)
		}
	}
}

// ---- sea_pickle ----

// TestSeaPicklePlacement: sea_pickle places up to `count` pickles on the ocean floor; the
// pickle-count draw (nextInt(4)+1) precedes the water/canSurvive test (draw order), matched
// by a parallel oracle.
func TestSeaPicklePlacement(t *testing.T) {
	const floorY = 50
	bctx, view := vegBctx()
	fillSeaColumn(view, floorY, 6)
	origin := placement.BlockPos{X: 8, Y: floorY + 3, Z: 8}
	cf := vegCF(t, "sea_pickle", `{"count":10}`)

	const seed = int64(0x9C4)
	rng := levelgen.NewWorldgenRandom(seed)
	if !seaPickleBody(bctx, cf, nil, rng, origin) {
		t.Fatalf("sea_pickle returned false over a water floor")
	}

	// Oracle: count constant 10 (0 draws), then per i: x(2), z(2), pickles(1). y is fixed
	// at floorY+1 (ocean floor). Assert at least one pickle landed and the draw sequence
	// matches.
	oracle := levelgen.NewWorldgenRandom(seed)
	count := 10
	placed := 0
	for i := 0; i < count; i++ {
		x := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
		z := int(oracle.NextIntN(8)) - int(oracle.NextIntN(8))
		pickles := int(oracle.NextIntN(4)) + 1
		gx, gz := origin.X+x, origin.Z+z
		want := block.ToStateID[block.SeaPickle{Pickles: block.Integer(pickles), Waterlogged: false}]
		if view.GetBlock(gx, floorY+1, gz) == want {
			placed++
		}
	}
	if placed == 0 {
		t.Fatalf("sea_pickle placed no pickles matching the oracle draw sequence")
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("sea_pickle draw sequence diverged from the jar-order oracle")
	}
}

// ---- vines ----

// TestVinesOnWall: vines attach to a full-block wall to one side. Place a stone wall to the
// north of an empty origin; the vine should get its NORTH face set and place at origin.
func TestVinesOnWall(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 4, Y: 60, Z: 4}
	// Wall to the north (dz -1). Direction.values() = DOWN,UP,NORTH,... so NORTH is the
	// first horizontal wall encountered after UP; put the wall only to the north so NORTH wins.
	stone := block.ToStateID[block.Stone{}]
	view.SetBlock(origin.X, origin.Y, origin.Z-1, stone)

	rng := levelgen.NewWorldgenRandom(1)
	if !vinesBody(bctx, nil, nil, rng, origin) {
		t.Fatalf("vines returned false with a north wall")
	}
	want := block.ToStateID[block.Vine{North: true}]
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != want {
		t.Fatalf("vine NORTH face not placed: got %d want %d", got, want)
	}
}

// TestVinesUpFirst: with a full block ABOVE (UP is checked before NORTH), the vine attaches
// UP (Direction.values() order UP before the horizontals). Confirms the ordinal ordering.
func TestVinesUpFirst(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 6, Y: 60, Z: 6}
	stone := block.ToStateID[block.Stone{}]
	view.SetBlock(origin.X, origin.Y+1, origin.Z, stone) // block above (UP neighbour)
	view.SetBlock(origin.X, origin.Y, origin.Z-1, stone) // also a north wall

	rng := levelgen.NewWorldgenRandom(1)
	vinesBody(bctx, nil, nil, rng, origin)
	want := block.ToStateID[block.Vine{Up: true}]
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != want {
		t.Fatalf("vine did not attach UP first (Direction.values order): got %d want %d", got, want)
	}
}

// TestVinesNoAttach: with no full-block neighbour, no vine is placed.
func TestVinesNoAttach(t *testing.T) {
	bctx, view := vegBctx()
	origin := placement.BlockPos{X: 8, Y: 60, Z: 8}
	rng := levelgen.NewWorldgenRandom(1)
	if vinesBody(bctx, nil, nil, rng, origin) {
		t.Fatalf("vines placed with no attachable neighbour")
	}
	if !block.IsAir(view.GetBlock(origin.X, origin.Y, origin.Z)) {
		t.Fatalf("vines wrote a block despite no attachment")
	}
}

// ---- bamboo ----

// TestBambooStalk: bamboo grows a stalk of height nextInt(12)+5 over a dirt floor, capped
// with the large/small leaf blocks when tall enough. Assert the trunk + tip states + the
// draw order (height, probability, [podzol radius]).
func TestBambooStalk(t *testing.T) {
	const floorY = 60
	bctx, view := vegBctx()
	fillLandFloor(view, floorY)
	origin := placement.BlockPos{X: 4, Y: floorY + 1, Z: 4}
	// probability 0.0 -> no podzol patch (no radius draw); simplest draw accounting.
	cf := vegCF(t, "bamboo", `{"probability":0.0}`)

	const seed = int64(0xBA30)
	rng := levelgen.NewWorldgenRandom(seed)
	if !bambooBody(bctx, cf, nil, rng, origin) {
		t.Fatalf("bamboo returned false over an empty air cell on dirt")
	}

	oracle := levelgen.NewWorldgenRandom(seed)
	height := int(oracle.NextIntN(12)) + 5
	_ = oracle.NextFloat() // probability draw (0.0 -> never < 0.0, no radius draw)

	trunk := block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesNone, Stage: 0}]
	// The base cell is the trunk.
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != trunk {
		t.Fatalf("bamboo base is not a trunk block: got %d want %d", got, trunk)
	}
	// The stalk grew `height` trunk cells (all air above the floor), so top-cap applies
	// (height>=5 >= 3): final large at origin.Y+height.
	finalLarge := block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesLarge, Stage: 1}]
	topLarge := block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesLarge, Stage: 0}]
	topSmall := block.ToStateID[block.Bamboo{Age: 1, Leaves: block.BambooLeavesSmall, Stage: 0}]
	if got := view.GetBlock(origin.X, origin.Y+height, origin.Z); got != finalLarge {
		t.Fatalf("bamboo final-large tip missing at y+height=%d: got %d want %d", origin.Y+height, got, finalLarge)
	}
	if got := view.GetBlock(origin.X, origin.Y+height-1, origin.Z); got != topLarge {
		t.Fatalf("bamboo top-large missing: got %d want %d", got, topLarge)
	}
	if got := view.GetBlock(origin.X, origin.Y+height-2, origin.Z); got != topSmall {
		t.Fatalf("bamboo top-small missing: got %d want %d", got, topSmall)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("bamboo draw sequence diverged from the jar-order oracle (probability 0 path)")
	}
}

// TestBambooPodzolDraw: with probability 1.0 the podzol radius draw (nextInt(4)+1) is
// consumed AFTER the probability draw. Assert the draw order (height, prob, radius) via a
// parallel oracle over a dirt floor whose surface is beneath_bamboo_podzol_replaceable.
func TestBambooPodzolDraw(t *testing.T) {
	const floorY = 70
	bctx, view := vegBctx()
	fillLandFloor(view, floorY)
	origin := placement.BlockPos{X: 8, Y: floorY + 1, Z: 8}
	cf := vegCF(t, "bamboo", `{"probability":1.0}`)

	const seed = int64(0xB0D2)
	rng := levelgen.NewWorldgenRandom(seed)
	bambooBody(bctx, cf, nil, rng, origin)

	oracle := levelgen.NewWorldgenRandom(seed)
	_ = int(oracle.NextIntN(12)) + 5 // height
	_ = oracle.NextFloat()           // probability (1.0 path always enters)
	r := int(oracle.NextIntN(4)) + 1 // podzol radius
	if r < 1 || r > 4 {
		t.Fatalf("bad podzol radius %d", r)
	}
	// Podzol should have replaced the dirt surface (WORLD_SURFACE-1) at the origin column
	// (distance 0 <= r*r). The surface heightmap top is floorY+1, so podzol is at floorY.
	podzol := block.DefaultStateID["minecraft:podzol"]
	if got := view.GetBlock(origin.X, floorY, origin.Z); got != podzol {
		t.Fatalf("bamboo did not lay podzol at the surface (probability 1.0): got %d want %d", got, podzol)
	}
	// The trailing draws must still line up (both consumed height+prob+radius, then the
	// stalk/podzol loop consume no rng — the podzol replaceable check + provider are draw-free).
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("bamboo podzol-path draw sequence diverged from the jar-order oracle")
	}
}
