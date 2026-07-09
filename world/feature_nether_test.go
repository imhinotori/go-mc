package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func fillNetherFloor(view *Neighborhood, floorY int, st block.StateID) {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := dx*16, dz*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					view.SetBlock(bx+lx, floorY, bz+lz, st)
				}
			}
		}
	}
}

func countBlockID(view *Neighborhood, origin placement.BlockPos, id string, reach, yLo, yHi int) int {
	count := 0
	for y := origin.Y + yLo; y <= origin.Y+yHi; y++ {
		for z := origin.Z - reach; z <= origin.Z+reach; z++ {
			for x := origin.X - reach; x <= origin.X+reach; x++ {
				st := view.GetBlock(x, y, z)
				if int(st) >= 0 && int(st) < len(block.StateList) && block.StateList[st].ID() == id {
					count++
				}
			}
		}
	}
	return count
}

func assertNetherViewsEqual(t *testing.T, a, b *Neighborhood, origin placement.BlockPos, reach, yLo, yHi int) {
	t.Helper()
	for y := origin.Y + yLo; y <= origin.Y+yHi; y++ {
		for z := origin.Z - reach; z <= origin.Z+reach; z++ {
			for x := origin.X - reach; x <= origin.X+reach; x++ {
				if a.GetBlock(x, y, z) != b.GetBlock(x, y, z) {
					t.Fatalf("view mismatch at (%d,%d,%d): %d != %d", x, y, z, a.GetBlock(x, y, z), b.GetBlock(x, y, z))
				}
			}
		}
	}
}

func TestGlowstoneBlobPlacesAndDrawOrder(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:glowstone_extra")

	view := build3x3([2]int{0, 0}, minY, height)
	view.SetBlock(origin.X, origin.Y+1, origin.Z, netherrackID)
	rng := levelgen.NewWorldgenRandom(0x6105)
	if !glowstoneBlobBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), rng, origin) {
		t.Fatalf("glowstone_blob rejected air under netherrack")
	}
	if got := view.GetBlock(origin.X, origin.Y, origin.Z); got != glowstoneID {
		t.Fatalf("origin glowstone missing: got %d want %d", got, glowstoneID)
	}

	oracle := levelgen.NewWorldgenRandom(0x6105)
	for i := 0; i < 1500; i++ {
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(12)
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(8)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("glowstone_blob draw sequence diverged from the javap loop oracle")
	}
}

func TestWeepingVinesNetherPlacementDeterministic(t *testing.T) {
	const minY, height = -64, 384
	origin := placement.BlockPos{X: 8, Y: 72, Z: 8}
	cf := resolveCF(t, "minecraft:weeping_vines")

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		view.SetBlock(origin.X, origin.Y+1, origin.Z, netherrackID)
		return view
	}

	seed := int64(-1)
	for s := int64(1); s < 80; s++ {
		view := build()
		weepingVinesBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(s), origin)
		if countBlockID(view, origin, "minecraft:weeping_vines", 8, -18, 2) > 0 {
			seed = s
			break
		}
	}
	if seed < 0 {
		t.Fatalf("weeping_vines placed no vine heads across searched seeds")
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(seed)
	if !weepingVinesBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("weeping_vines rejected a valid netherrack roof")
	}
	v2 := build()
	r2 := levelgen.NewWorldgenRandom(seed)
	weepingVinesBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)

	assertNetherViewsEqual(t, v1, v2, origin, 9, -20, 3)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("weeping_vines post-place rng fingerprint diverged")
	}
	if got := countBlockID(v1, origin, "minecraft:nether_wart_block", 8, -8, 2); got == 0 {
		t.Fatalf("weeping_vines placed no roof nether_wart_block")
	}
}

func TestTwistingVinesNetherPlacementDeterministic(t *testing.T) {
	const minY, height, floorY = -64, 384, 64
	origin := placement.BlockPos{X: 8, Y: floorY + 1, Z: 8}
	cf := resolveCF(t, "minecraft:twisting_vines")

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		fillNetherFloor(view, floorY, warpedNyliumID)
		return view
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0x7A157)
	if !twistingVinesBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("twisting_vines rejected air over warped nylium")
	}
	if countBlockID(v1, origin, "minecraft:twisting_vines", 10, 0, 24) == 0 {
		t.Fatalf("twisting_vines placed no vine heads")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0x7A157)
	twistingVinesBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 10, 0, 26)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("twisting_vines post-place rng fingerprint diverged")
	}
}

func TestNetherForestVegetationSimpleProviderDrawOrder(t *testing.T) {
	const minY, height, floorY = -64, 384, 64
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:nether_sprouts")
	if err != nil {
		t.Fatalf("resolve nether_sprouts: %v", err)
	}
	origin := placement.BlockPos{X: 8, Y: floorY + 1, Z: 8}
	view := build3x3([2]int{0, 0}, minY, height)
	fillNetherFloor(view, floorY, warpedNyliumID)

	const seed = int64(0xF01257)
	rng := levelgen.NewWorldgenRandom(seed)
	if !netherForestVegetationBody(&bodyContext{view: view, reg: reg}, cf, newPlacementContext(view, minY, height, nil), rng, origin) {
		t.Fatalf("nether_forest_vegetation placed no nether_sprouts over nylium")
	}
	if countBlockID(view, origin, "minecraft:nether_sprouts", 8, 0, 1) == 0 {
		t.Fatalf("nether_forest_vegetation did not write nether_sprouts")
	}

	oracle := levelgen.NewWorldgenRandom(seed)
	for i := 0; i < 8*8; i++ {
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(4)
		_ = oracle.NextIntN(4)
		_ = oracle.NextIntN(8)
		_ = oracle.NextIntN(8)
	}
	if rng.NextLong() != oracle.NextLong() {
		t.Fatalf("nether_forest_vegetation simple-provider draw sequence diverged")
	}
}

func TestNetherForestVegetationWeightedConfigDeterministic(t *testing.T) {
	const minY, height, floorY = -64, 384, 64
	reg := feature.NewEmbeddedRegistry()
	cf, err := reg.ResolveConfigured("minecraft:crimson_forest_vegetation")
	if err != nil {
		t.Fatalf("resolve crimson_forest_vegetation: %v", err)
	}
	origin := placement.BlockPos{X: 8, Y: floorY + 1, Z: 8}

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		fillNetherFloor(view, floorY, crimsonNyliumID)
		return view
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0xC21A150)
	if !netherForestVegetationBody(&bodyContext{view: v1, reg: reg}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("crimson_forest_vegetation placed nothing over crimson nylium")
	}
	if countBlockID(v1, origin, "minecraft:crimson_roots", 8, 0, 1) == 0 {
		t.Fatalf("crimson_forest_vegetation placed no crimson_roots")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0xC21A150)
	netherForestVegetationBody(&bodyContext{view: v2, reg: reg}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	assertNetherViewsEqual(t, v1, v2, origin, 8, 0, 2)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("crimson_forest_vegetation post-place rng fingerprint diverged")
	}
}
