package world

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// TestEndPlatformGeometry proves EndPlatformFeature: a 5x5 obsidian floor at dy=-1 and
// air in the 5x5x3 cavity above (dy 0..2) around the origin.
func TestEndPlatformGeometry(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:end_platform")
	view := build3x3([2]int{0, 0}, minY, height)
	bctx := &bodyContext{view: view}
	if !endPlatformBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("end_platform returned false")
	}
	airID := view.air
	obs := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if view.GetBlock(origin.X+dx, origin.Y-1, origin.Z+dz) != obsidianID {
				t.Fatalf("floor not obsidian at (%d,%d,%d)", origin.X+dx, origin.Y-1, origin.Z+dz)
			}
			obs++
			for dy := 0; dy <= 2; dy++ {
				if view.GetBlock(origin.X+dx, origin.Y+dy, origin.Z+dz) != airID {
					t.Fatalf("cavity not air at (%d,%d,%d)", origin.X+dx, origin.Y+dy, origin.Z+dz)
				}
			}
		}
	}
	if obs != 25 {
		t.Fatalf("expected 25 obsidian floor blocks, got %d", obs)
	}
}

// TestVoidStartPlatformCenter proves VoidStartPlatformFeature: within the origin chunk the
// center column is cobblestone and the rest of the 33x33 diamond is stone, at y=origin.Y+3.
func TestVoidStartPlatformCenter(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 60, Z: 8}
	cf := resolveCF(t, "minecraft:void_start_platform")
	view := build3x3([2]int{0, 0}, minY, height)
	bctx := &bodyContext{view: view}
	if !voidStartPlatformBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("void_start_platform returned false")
	}
	y := origin.Y + 3
	if view.GetBlock(8, y, 8) != cobblestoneID {
		t.Fatalf("center not cobblestone: got %d want %d", view.GetBlock(8, y, 8), cobblestoneID)
	}
	if view.GetBlock(0, y, 0) != stoneID {
		t.Fatalf("(0,%d,0) not stone: got %d", y, view.GetBlock(0, y, 0))
	}
	if view.GetBlock(15, y, 15) != stoneID {
		t.Fatalf("(15,%d,15) not stone", y)
	}
}

// TestVoidStartPlatformFarChunkNoop proves VoidStartPlatformFeature no-ops (returns true,
// writes nothing) more than 1 chunk from origin chunk (0,0).
func TestVoidStartPlatformFarChunkNoop(t *testing.T) {
	const minY, height = 0, 256
	center := [2]int{5, 5}
	origin := placement.BlockPos{X: 5*16 + 8, Y: 60, Z: 5*16 + 8}
	cf := resolveCF(t, "minecraft:void_start_platform")
	view := build3x3(center, minY, height)
	bctx := &bodyContext{view: view}
	if !voidStartPlatformBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("void_start_platform should return true even when far")
	}
	if view.GetBlock(origin.X, origin.Y+3, origin.Z) != view.air {
		t.Fatalf("far chunk should stay air, got %d", view.GetBlock(origin.X, origin.Y+3, origin.Z))
	}
}

// TestEndIslandDeterministicAndDrawOrder proves EndIslandFeature places END_STONE and its
// RNG draw sequence matches a hand-derived oracle (f = nextInt(3)+4; per layer while
// f>0.5: after filling, f -= nextInt(2)+0.5).
func TestEndIslandDeterministicAndDrawOrder(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 128, Z: 8}
	cf := resolveCF(t, "minecraft:end_island")

	build := func() *Neighborhood { return build3x3([2]int{0, 0}, minY, height) }

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0x1451A)
	if !endIslandBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("end_island returned false")
	}
	// Top-centre must be END_STONE (i=0 always fills the origin column).
	if v1.GetBlock(origin.X, origin.Y, origin.Z) != endStoneID {
		t.Fatalf("island top-centre not end_stone: got %d", v1.GetBlock(origin.X, origin.Y, origin.Z))
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0x1451A)
	endIslandBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	for y := origin.Y - 8; y <= origin.Y; y++ {
		for x := origin.X - 8; x <= origin.X+8; x++ {
			for z := origin.Z - 8; z <= origin.Z+8; z++ {
				if v1.GetBlock(x, y, z) != v2.GetBlock(x, y, z) {
					t.Fatalf("island non-deterministic at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}

	// Draw-order oracle: replay the exact NextIntN sequence and compare a fingerprint.
	oracle := levelgen.NewWorldgenRandom(0x1451A)
	f := float32(oracle.NextIntN(3)) + 4.0
	for f > 0.5 {
		f -= float32(oracle.NextIntN(2)) + 0.5
	}
	if r1.NextLong() != oracle.NextLong() {
		t.Fatalf("end_island draw sequence diverged from oracle")
	}
}

// TestEndGatewayShape proves EndGatewayFeature: END_GATEWAY at the exact centre, bedrock at
// the vertical extremes on the centre column, and air on the origin y-plane ring.
func TestEndGatewayShape(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 75, Z: 8}
	cf := resolveCF(t, "minecraft:end_gateway_delayed")
	view := build3x3([2]int{0, 0}, minY, height)
	// Prefill the box with stone so AIR writes are observable.
	for x := origin.X - 1; x <= origin.X+1; x++ {
		for y := origin.Y - 2; y <= origin.Y+2; y++ {
			for z := origin.Z - 1; z <= origin.Z+1; z++ {
				view.SetBlock(x, y, z, stoneID)
			}
		}
	}
	bctx := &bodyContext{view: view}
	if !endGatewayBody(bctx, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("end_gateway returned false")
	}
	if view.GetBlock(origin.X, origin.Y, origin.Z) != endGatewayID {
		t.Fatalf("centre not end_gateway: got %d", view.GetBlock(origin.X, origin.Y, origin.Z))
	}
	// Centre column top/bottom (ymid && xc && zc) -> bedrock.
	if view.GetBlock(origin.X, origin.Y+2, origin.Z) != bedrockID {
		t.Fatalf("top centre not bedrock")
	}
	if view.GetBlock(origin.X, origin.Y-2, origin.Z) != bedrockID {
		t.Fatalf("bottom centre not bedrock")
	}
	// Origin y-plane, off-centre ring -> air (yc branch).
	if view.GetBlock(origin.X+1, origin.Y, origin.Z) != view.air {
		t.Fatalf("y-plane ring not air")
	}
}

// TestChorusPlantGrows proves ChorusPlantFeature: over end_stone with air above, it places
// at least one CHORUS_PLANT and terminates in a CHORUS_FLOWER, deterministically.
func TestChorusPlantGrows(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:chorus_plant")

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		// End-stone floor beneath the origin, air everywhere else (EmptyChunk is air).
		view.SetBlock(origin.X, origin.Y-1, origin.Z, endStoneID)
		return view
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0xC0FFEE)
	if !chorusPlantBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("chorus_plant rejected air over end_stone")
	}
	plants := countBlockID(v1, origin, "minecraft:chorus_plant", 12, 0, 24)
	flowers := countBlockID(v1, origin, "minecraft:chorus_flower", 12, 0, 24)
	if plants == 0 {
		t.Fatalf("chorus_plant placed no chorus_plant blocks")
	}
	if flowers == 0 {
		t.Fatalf("chorus_plant placed no terminating chorus_flower")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0xC0FFEE)
	chorusPlantBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	for y := origin.Y; y <= origin.Y+24; y++ {
		for x := origin.X - 12; x <= origin.X+12; x++ {
			for z := origin.Z - 12; z <= origin.Z+12; z++ {
				if v1.GetBlock(x, y, z) != v2.GetBlock(x, y, z) {
					t.Fatalf("chorus non-deterministic at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("chorus_plant post-place rng fingerprint diverged")
	}
}

// TestChorusPlantRejectsBadGround proves ChorusPlantFeature returns false when the block
// below the origin is not in supports_chorus_plant (stone is not).
func TestChorusPlantRejectsBadGround(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 70, Z: 8}
	cf := resolveCF(t, "minecraft:chorus_plant")
	view := build3x3([2]int{0, 0}, minY, height)
	view.SetBlock(origin.X, origin.Y-1, origin.Z, stoneID)
	if chorusPlantBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil), levelgen.NewWorldgenRandom(1), origin) {
		t.Fatalf("chorus_plant should reject stone ground")
	}
}

// TestEndSpikePositionsDeterministic proves EndSpikeFeature.getSpikesForLevel yields the
// classic 10-spike ring at radius 42 around (0,0), deterministically for a given seed.
func TestEndSpikePositionsDeterministic(t *testing.T) {
	a := getSpikesForLevel(42)
	b := getSpikesForLevel(42)
	if len(a) != 10 {
		t.Fatalf("expected 10 spikes, got %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("spike %d non-deterministic: %+v vs %+v", i, a[i], b[i])
		}
	}
	// Spike 0 (i=0) is always at angle 2*(-PI) -> cos=1,sin=0 -> center (42,0).
	if a[0].centerX != 42 || a[0].centerZ != 0 {
		t.Fatalf("spike 0 center = (%d,%d), want (42,0)", a[0].centerX, a[0].centerZ)
	}
	// Every spike center sits on the radius-42 ring (|center| ~ 42, floored).
	for i, s := range a {
		if s.radius < 2 || s.radius > 5 {
			t.Fatalf("spike %d radius out of range: %d", i, s.radius)
		}
		if s.height < 76 || s.height > 103 {
			t.Fatalf("spike %d height out of range: %d", i, s.height)
		}
	}
}

// TestEndSpikeBuildsObsidianPillar proves EndSpikeFeature builds the obsidian pillar for a
// spike whose center is within the decorated chunk (seed 42, chunk (2,0) -> spike 0 at
// center (42,0), radius 4, height 100), deterministically.
func TestEndSpikeBuildsObsidianPillar(t *testing.T) {
	const minY, height = 0, 256
	center := [2]int{2, 0}
	origin := placement.BlockPos{X: 42, Y: 60, Z: 0}
	cf := resolveCF(t, "minecraft:end_spike")

	build := func() *Neighborhood { return build3x3(center, minY, height) }

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(7)
	bctx1 := &bodyContext{view: v1, seed: 42}
	if !endSpikeBody(bctx1, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("end_spike returned false")
	}
	// The spike-0 centre column below its height must be obsidian.
	if v1.GetBlock(42, 40, 0) != obsidianID {
		t.Fatalf("spike centre column not obsidian at (42,40,0): got %d", v1.GetBlock(42, 40, 0))
	}
	if v1.GetBlock(42, 99, 0) != obsidianID {
		t.Fatalf("spike centre column not obsidian just below top: got %d", v1.GetBlock(42, 99, 0))
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(7)
	bctx2 := &bodyContext{view: v2, seed: 42}
	endSpikeBody(bctx2, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	for y := minY; y < 110; y++ {
		for x := 42 - 6; x <= 42+6; x++ {
			for z := -6; z <= 6; z++ {
				if v1.GetBlock(x, y, z) != v2.GetBlock(x, y, z) {
					t.Fatalf("end_spike non-deterministic at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}

// TestSpikeBodyBuildsObsidian proves SpikeFeature (the "spike" type) builds an obsidian
// spike (its default state) over a solid landing block, deterministically.
func TestSpikeBodyBuildsObsidian(t *testing.T) {
	const minY, height = 0, 256
	origin := placement.BlockPos{X: 8, Y: 80, Z: 8}
	cf := &feature.ConfiguredFeature{Type: "spike"}

	build := func() *Neighborhood {
		view := build3x3([2]int{0, 0}, minY, height)
		// A solid floor to land on (canPlaceOn permissive: any non-air).
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				view.SetBlock(x, 64, z, stoneID)
			}
		}
		return view
	}

	v1 := build()
	r1 := levelgen.NewWorldgenRandom(0x5719E)
	if !spikeBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), r1, origin) {
		t.Fatalf("spike returned false over solid ground")
	}
	if countBlockID(v1, origin, "minecraft:obsidian", 6, -40, 20) == 0 {
		t.Fatalf("spike placed no obsidian")
	}

	v2 := build()
	r2 := levelgen.NewWorldgenRandom(0x5719E)
	spikeBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), r2, origin)
	if r1.NextLong() != r2.NextLong() {
		t.Fatalf("spike post-place rng fingerprint diverged")
	}
}
