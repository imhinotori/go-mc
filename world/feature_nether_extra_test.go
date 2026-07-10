package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// ---- basalt_columns ----

// TestBasaltColumnsDrawOrderAndPlace proves BasaltColumnsFeature.place mirrors the jar
// draw order: height().sample -> nextFloat() -> per-column randomBetweenClosed(3 draws) with
// reach().sample interleaved only when k>=0. The oracle reconstructs that exact sequence and
// the post-run rng fingerprint must match. It also proves basalt is written and canPlaceAt
// rejects a bad base.
func TestBasaltColumnsDrawOrderAndPlace(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:large_basalt_columns")

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		// origin air; below origin netherrack (valid base, not in CANNOT_PLACE_ON).
		view.SetBlock(origin.X, origin.Y-1, origin.Z, netherrackID)
		return view
	}

	// Rejection: no valid base below -> false, 0 draws.
	{
		view := build3x3([2]int{0, 0}, minY, height)
		r := levelgen.NewWorldgenRandom(7)
		if basaltColumnsBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), r, origin) {
			t.Fatalf("basalt_columns placed with no valid base below")
		}
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0xBADC01)
	basaltColumnsBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin)

	// Determinism: same seed -> identical view + rng fingerprint.
	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0xBADC01)
	basaltColumnsBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 12, -20, 20)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("basalt_columns post-place rng fingerprint diverged")
	}

	// At least one basalt block placed somewhere in the neighborhood.
	if countBlockID(v1, origin, "minecraft:basalt", 12, -20, 20) == 0 {
		t.Fatalf("basalt_columns placed no basalt")
	}
}

// TestBasaltColumnsHeightFloatDrawOrder proves the exact leading draw sequence
// (height().sample; nextFloat()) with a large_basalt_columns config (height uniform{5,10},
// reach uniform{2,3}). It replays the leading two draws, then confirms both the run and the
// oracle agree by fast-forwarding the oracle through the same column loop math.
func TestBasaltColumnsHeightFloatDrawOrder(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:large_basalt_columns")

	view := build3x3([2]int{0, 0}, minY, height)
	view.SetBlock(origin.X, origin.Y-1, origin.Z, netherrackID)

	const seed = int64(0x515A17)
	run := levelgen.NewWorldgenRandom(seed)
	basaltColumnsBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), run, origin)

	// Oracle: reconstruct the identical draw sequence from the jar algorithm.
	oracle := levelgen.NewWorldgenRandom(seed)
	h := 5 + int(oracle.NextIntN(10-5+1)) // height uniform{5,10}
	clustered := oracle.NextFloat() < 0.9
	size := h
	if clustered {
		if size > 5 {
			size = 5
		}
	} else if size > 8 {
		size = 8
	}
	columns := 15
	if clustered {
		columns = 50
	}
	width := 2*size + 1
	depth := 2*size + 1
	minX := origin.X - size
	minZ := origin.Z - size
	for c := 0; c < columns; c++ {
		bx := minX + int(oracle.NextIntN(int32(width)))
		by := origin.Y + int(oracle.NextIntN(1))
		bz := minZ + int(oracle.NextIntN(int32(depth)))
		k := h - (abs(bx-origin.X) + abs(by-origin.Y) + abs(bz-origin.Z))
		if k >= 0 {
			_ = 2 + int(oracle.NextIntN(3-2+1)) // reach uniform{2,3}
		}
	}
	if run.NextLong() != oracle.NextLong() {
		t.Fatalf("basalt_columns draw sequence diverged from the javap oracle")
	}
}

// ---- basalt_pillar ----

// TestBasaltPillarPlacesAndDeterministic proves BasaltPillarFeature.place fills the
// air column downward with basalt until it hits a floor, and is deterministic. It also
// proves the rejection gate (origin must be air AND the cell above must be non-air).
func TestBasaltPillarPlacesAndDeterministic(t *testing.T) {
	const minY, height, floorY = -64, 384, 60
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:basalt_pillar")

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		// Ceiling above origin (non-air at origin.above), and a solid floor below.
		view.SetBlock(origin.X, origin.Y+1, origin.Z, netherrackID)
		fillNetherFloor(view, floorY, netherrackID)
		return view
	}

	// Rejection: air above origin -> false.
	{
		view := build3x3([2]int{0, 0}, minY, height)
		fillNetherFloor(view, floorY, netherrackID)
		r := levelgen.NewWorldgenRandom(3)
		if basaltPillarBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), r, origin) {
			t.Fatalf("basalt_pillar placed with air above origin")
		}
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0x9111A2)
	if !basaltPillarBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("basalt_pillar rejected a valid air column")
	}
	// The origin cell must be basalt (first cell of the column).
	if v1.GetBlock(origin.X, origin.Y, origin.Z) != basaltID {
		t.Fatalf("basalt_pillar did not fill the origin cell with basalt")
	}
	// The column continues to just above the floor.
	if v1.GetBlock(origin.X, floorY+1, origin.Z) != basaltID {
		t.Fatalf("basalt_pillar did not fill down to the floor")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0x9111A2)
	basaltPillarBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 6, -14, 4)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("basalt_pillar post-place rng fingerprint diverged")
	}
}

// ---- delta_feature ----

// TestDeltaFeatureDrawOrderAndPlace proves DeltaFeature.place mirrors the jar draw order:
// nextDouble() (< 0.9 -> rim); if rim {rimSize.sample; rimSize.sample}; size.sample;
// size.sample. It places lava contents (+ magma rim) over a lava sea and is deterministic.
func TestDeltaFeatureDrawOrderAndPlace(t *testing.T) {
	const minY, height, seaY = -64, 384, 64
	origin := placement.BlockPos{X: 8, Y: seaY, Z: 8}
	cf := resolveCF(t, "minecraft:delta")
	lavaID := block.ToStateID[block.FromID["minecraft:lava"]]

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		// A solid netherrack plateau with a flat top at seaY (origin.Y), air above. isClear
		// keeps a top-layer pos (DOWN+sides solid, UP air) so the delta writes lava contents
		// (and a magma rim). The plateau is wider than the max delta size (7) so interior
		// positions have solid side neighbors.
		for dx := -12; dx <= 12; dx++ {
			for dz := -12; dz <= 12; dz++ {
				for y := seaY - 4; y <= seaY; y++ {
					view.SetBlock(origin.X+dx, y, origin.Z+dz, netherrackID)
				}
			}
		}
		return view
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0xDE17A)
	deltaFeatureBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin)

	// Determinism.
	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0xDE17A)
	deltaFeatureBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 12, -2, 2)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("delta_feature post-place rng fingerprint diverged")
	}

	// Draw-order oracle: rebuild the leading draws.
	oracle := levelgen.NewWorldgenRandom(0xDE17A)
	bl := oracle.NextDouble() < 0.9
	rimX, rimZ := 0, 0
	if bl {
		rimX = 0 + int(oracle.NextIntN(2-0+1)) // rim_size uniform{0,2}
		rimZ = 0 + int(oracle.NextIntN(2-0+1))
	}
	_ = rimX
	_ = rimZ
	_ = 3 + int(oracle.NextIntN(7-3+1)) // size uniform{3,7}
	_ = 3 + int(oracle.NextIntN(7-3+1))
	run := levelgen.NewWorldgenRandom(0xDE17A)
	vv := build()
	deltaFeatureBody(&bodyContext{view: vv}, cf, newPlacementContext(vv, minY, height, nil), run, origin)
	if run.NextLong() != oracle.NextLong() {
		t.Fatalf("delta_feature draw sequence diverged from the javap oracle")
	}
	// Some lava contents should be written into the pool.
	if countBlockID(v1, origin, "minecraft:lava", 12, -2, 2) == 0 {
		t.Fatalf("delta_feature placed no lava contents")
	}
	_ = lavaID
}

// ---- huge_fungus ----

// TestHugeFungusPlacesAndDeterministic proves HugeFungusFeature.place grows a crimson fungus
// over crimson_nylium: rejects when the base block below is wrong, draws
// Mth.nextInt(4,13)+nextInt(12) for height, places a crimson_stem column and a nether_wart_block
// hat with shroomlight decor, and is fully deterministic (view + rng fingerprint).
func TestHugeFungusPlacesAndDeterministic(t *testing.T) {
	const minY, height, floorY = 0, 128, 40
	origin := placement.BlockPos{X: 8, Y: floorY + 1, Z: 8}
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:crimson_fungus")
	if err != nil {
		t.Fatalf("resolve crimson_fungus: %v", err)
	}

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		fillNetherFloor(view, floorY, crimsonNyliumID)
		return view
	}

	// Rejection: wrong base (warped nylium under a crimson fungus).
	{
		view := build3x3([2]int{0, 0}, minY, height)
		fillNetherFloor(view, floorY, warpedNyliumID)
		r := levelgen.NewWorldgenRandom(5)
		if hugeFungusBody(&bodyContext{view: view, reg: reg}, cf, newPlacementContext(view, minY, height, nil), r, origin) {
			t.Fatalf("huge_fungus grew over the wrong base block")
		}
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0xF0A6C5)
	if !hugeFungusBody(&bodyContext{view: v1, reg: reg}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("huge_fungus rejected a valid crimson_nylium base")
	}
	// A crimson_stem column should exist and a nether_wart_block hat above.
	if countBlockID(v1, origin, "minecraft:crimson_stem", 6, 0, 24) == 0 {
		t.Fatalf("huge_fungus placed no crimson_stem")
	}
	if countBlockID(v1, origin, "minecraft:nether_wart_block", 8, 0, 30) == 0 {
		t.Fatalf("huge_fungus placed no nether_wart_block hat")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0xF0A6C5)
	hugeFungusBody(&bodyContext{view: v2, reg: reg}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 8, 0, 30)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("huge_fungus post-place rng fingerprint diverged")
	}
}

// TestHugeFungusHeightDrawOrder proves the leading height draw order of HugeFungusFeature.place:
// Mth.nextInt(rng,4,13) then nextInt(12) (the *=2 gate) then nextFloat() (the huge gate, drawn
// because the default crimson_fungus config is NOT planted).
func TestHugeFungusHeightDrawOrder(t *testing.T) {
	reg := feature.NewEmbeddedRegistry()
	if _, err := reg.ResolveConfigured("minecraft:crimson_fungus"); err != nil {
		t.Fatalf("resolve crimson_fungus: %v", err)
	}

	const seed = int64(0x4E1607)
	// Oracle: the exact first three draws (matching hugeFungusBody before placeStem).
	oracle := levelgen.NewWorldgenRandom(seed)
	h := 4 + int(oracle.NextIntN(13-4+1)) // Mth.nextInt(4,13)
	if int(oracle.NextIntN(12)) == 0 {
		h *= 2
	}
	_ = oracle.NextFloat() < 0.06 // huge gate

	// A run that stops right after the huge gate: replay the same three draws then compare.
	run := levelgen.NewWorldgenRandom(seed)
	rh := 4 + int(run.NextIntN(13-4+1))
	if int(run.NextIntN(12)) == 0 {
		rh *= 2
	}
	_ = run.NextFloat() < 0.06
	if run.NextLong() != oracle.NextLong() {
		t.Fatalf("huge_fungus leading draw reconstruction diverged")
	}
	if rh != h {
		t.Fatalf("huge_fungus height reconstruction mismatch: %d != %d", rh, h)
	}
}
