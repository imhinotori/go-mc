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

// TestOceanFloorLiveComputed proves the LIVE_WORLD OCEAN_FLOOR map (id 3) is populated
// non-zero by BuildWorldgenHeightmaps (previously allocated + persisted but left all-zero,
// making spawn.go's ocean-reject dead). A stone column to y=64 -> OceanFloor top y=65.
func TestOceanFloorLiveComputed(t *testing.T) {
	const (
		secs = 24
		minY = -64
		lx   = 4
		lz   = 4
		yk   = 64
	)
	maxY := minY + secs*16
	col := lz<<4 | lx
	stone := block.ToStateID[block.Stone{}]
	ch := level.EmptyChunk(secs)
	for y := minY; y <= yk; y++ {
		setColumnBlock(ch, lx, y, lz, minY, stone)
	}
	BuildWorldgenHeightmaps(ch, minY, maxY)
	if got := ch.HeightMaps.OceanFloor.Get(col); got == 0 {
		t.Fatalf("LIVE OCEAN_FLOOR is 0 (dead); want non-zero for a stone column")
	}
	if got := ch.HeightMaps.OceanFloor.Get(col) + minY; got != yk+1 {
		t.Fatalf("LIVE OCEAN_FLOOR = %d, want %d (above stone top)", got, yk+1)
	}
}

// TestMotionBlockingSkipsVegetation proves the motion-blocking heightmaps use blocksMotion()
// (NOT !isAir): a non-colliding plant sitting on the stone top does NOT raise OCEAN_FLOOR /
// MOTION_BLOCKING, though it DOES raise WORLD_SURFACE_WG (NOT_AIR). This is the bug-1 fix.
func TestMotionBlockingSkipsVegetation(t *testing.T) {
	const (
		secs = 24
		minY = -64
		lx   = 6
		lz   = 6
		yk   = 64
	)
	maxY := minY + secs*16
	col := lz<<4 | lx
	stone := block.ToStateID[block.Stone{}]
	grass := block.ToStateID[block.ShortGrass{}]
	ch := level.EmptyChunk(secs)
	for y := minY; y <= yk; y++ {
		setColumnBlock(ch, lx, y, lz, minY, stone)
	}
	setColumnBlock(ch, lx, yk+1, lz, minY, grass) // plant on the stone top
	BuildWorldgenHeightmaps(ch, minY, maxY)

	if got := ch.HeightMaps.WorldSurfaceWG.Get(col) + minY; got != yk+2 {
		t.Fatalf("WORLD_SURFACE_WG = %d, want %d (above the plant)", got, yk+2)
	}
	if got := ch.HeightMaps.OceanFloorWG.Get(col) + minY; got != yk+1 {
		t.Fatalf("OCEAN_FLOOR_WG = %d, want %d (above stone; the plant does not block motion)", got, yk+1)
	}
	if got := ch.HeightMaps.MotionBlocking.Get(col) + minY; got != yk+1 {
		t.Fatalf("MOTION_BLOCKING = %d, want %d (plant is non-colliding)", got, yk+1)
	}
}

// TestSteepConditionBothAxes proves the SteepMaterialCondition tests BOTH X and Z (bug-6b):
// a WORLD_SURFACE_WG slope along X only (flat in Z) must still trip steep. Vanilla ORs the
// Z-axis pair with the X-axis pair. CITE: SteepMaterialCondition.compute.
func TestSteepConditionBothAxes(t *testing.T) {
	const (
		secs = 24
		minY = -64
	)
	ch := level.EmptyChunk(secs)
	// Build a WORLD_SURFACE_WG that is FLAT in Z but has a >=4 step across X at column
	// (lx=8): height at x=7 is high, x=9 is low. steep must fire on the X-axis test.
	// worldSurfaceHeight reads WorldSurfaceWG.Get(lz<<4|lx)+minY.
	for lz := 0; lz < 16; lz++ {
		for lx := 0; lx < 16; lx++ {
			h := 0
			if lx <= 7 {
				h = 10 // tall on the low-x side
			}
			ch.HeightMaps.WorldSurfaceWG.Set(lz<<4|lx, h)
		}
	}
	c := &Context{chunk: ch, minY: minY}
	c.blockX = 8 // xm=7 (h=10), xp=9 (h=0): h(xm) >= h(xp)+4 -> steep via X axis
	c.blockZ = 4
	if !(steepCondition{}).test(c) {
		t.Fatalf("steep did not fire on an X-only slope (h(xm)=10 vs h(xp)=0); X-axis test missing")
	}
	// A flat column (no step on either axis) must NOT be steep.
	for lz := 0; lz < 16; lz++ {
		for lx := 0; lx < 16; lx++ {
			ch.HeightMaps.WorldSurfaceWG.Set(lz<<4|lx, 5)
		}
	}
	c.blockX = 8
	if (steepCondition{}).test(c) {
		t.Fatalf("steep fired on a flat column, want false")
	}
}
