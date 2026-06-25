package surface

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// setColumnBlock writes a block at local (lx,lz) / world y into a chunk for the test.
func setColumnBlock(ch *level.Chunk, lx, worldY, lz, minY int, st block.StateID) {
	sec := (worldY - minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return
	}
	local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
	ch.Sections[sec].SetBlock(local, st)
}

// TestWorldgenHeightmapsBuild proves the 3 worldgen heightmaps build from final terrain
// with their DIVERGENT per-type predicates: a stone column capped by water then air.
//   - WORLD_SURFACE_WG (NOT_AIR)            → first Y above the water top.
//   - MOTION_BLOCKING (blocks-motion|fluid) → first Y above the water top (water blocks).
//   - OCEAN_FLOOR_WG (motion-blocking-no-fluid) → first Y above the STONE top (below water).
func TestWorldgenHeightmapsBuild(t *testing.T) {
	const (
		secs = 24
		minY = -64
		lx   = 7
		lz   = 9
		yk   = 64 // highest stone Y
	)
	maxY := minY + secs*16
	col := lz<<4 | lx

	stone := block.ToStateID[block.Stone{}]
	water := block.ToStateID[block.Water{Level: 0}]

	ch := level.EmptyChunk(secs)
	// Stone from minY+1 up to yk; water at yk+1..yk+3; air above. (minY is bedrock-ish,
	// but for the heightmap test only the top run matters.)
	for y := minY; y <= yk; y++ {
		setColumnBlock(ch, lx, y, lz, minY, stone)
	}
	for y := yk + 1; y <= yk+3; y++ {
		setColumnBlock(ch, lx, y, lz, minY, water)
	}

	BuildWorldgenHeightmaps(ch, minY, maxY)

	// WORLD_SURFACE_WG: first Y above the highest non-air block = water top yk+3 → yk+4.
	if got := ch.HeightMaps.WorldSurfaceWG.Get(col) + minY; got != yk+4 {
		t.Errorf("WORLD_SURFACE_WG = %d, want %d (above water top)", got, yk+4)
	}
	// MOTION_BLOCKING (worldgen): water blocks motion → same as WORLD_SURFACE_WG, yk+4.
	if got := ch.HeightMaps.MotionBlocking.Get(col) + minY; got != yk+4 {
		t.Errorf("MOTION_BLOCKING = %d, want %d (above water top)", got, yk+4)
	}
	// OCEAN_FLOOR_WG: highest motion-blocking-NO-FLUID = stone top yk → yk+1 (BELOW water).
	if got := ch.HeightMaps.OceanFloorWG.Get(col) + minY; got != yk+1 {
		t.Errorf("OCEAN_FLOOR_WG = %d, want %d (above stone top, below water)", got, yk+1)
	}

	// The per-predicate divergence is the point: OCEAN_FLOOR_WG must sit strictly below
	// WORLD_SURFACE_WG / MOTION_BLOCKING here.
	if ch.HeightMaps.OceanFloorWG.Get(col) >= ch.HeightMaps.WorldSurfaceWG.Get(col) {
		t.Errorf("OCEAN_FLOOR_WG (%d) should be below WORLD_SURFACE_WG (%d) for a water-capped column",
			ch.HeightMaps.OceanFloorWG.Get(col), ch.HeightMaps.WorldSurfaceWG.Get(col))
	}
}

// TestWorldgenHeightmapsDeterministic confirms the build is a pure top-down scan: two
// builds over the same blocks agree, and an all-air column floors all three to 0.
func TestWorldgenHeightmapsDeterministic(t *testing.T) {
	const (
		secs = 24
		minY = -64
	)
	maxY := minY + secs*16

	ch := level.EmptyChunk(secs)
	// Leave everything air (the EmptyChunk default). All heightmaps must floor to 0.
	BuildWorldgenHeightmaps(ch, minY, maxY)
	for col := 0; col < 16*16; col++ {
		if v := ch.HeightMaps.WorldSurfaceWG.Get(col); v != 0 {
			t.Fatalf("all-air WORLD_SURFACE_WG[%d] = %d, want 0", col, v)
		}
		if v := ch.HeightMaps.OceanFloorWG.Get(col); v != 0 {
			t.Fatalf("all-air OCEAN_FLOOR_WG[%d] = %d, want 0", col, v)
		}
		if v := ch.HeightMaps.MotionBlocking.Get(col); v != 0 {
			t.Fatalf("all-air MOTION_BLOCKING[%d] = %d, want 0", col, v)
		}
	}

	// A solid column → idempotent: build twice, identical result.
	stone := block.ToStateID[block.Stone{}]
	for col := 0; col < 16*16; col++ {
		lx, lz := col&15, col>>4
		setColumnBlock(ch, lx, 50, lz, minY, stone)
	}
	BuildWorldgenHeightmaps(ch, minY, maxY)
	first := ch.HeightMaps.WorldSurfaceWG.Get(0)
	BuildWorldgenHeightmaps(ch, minY, maxY)
	if second := ch.HeightMaps.WorldSurfaceWG.Get(0); second != first {
		t.Fatalf("non-idempotent build: %d then %d", first, second)
	}
}
