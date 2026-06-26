package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

// localOf maps a world coord back to its chunk-local 0..15 index (for reading the spawn
// chunk's blocks at the safe column the finder returned).
func localOf(world float64) int { return int(world-0.5) & 15 }

// TestSafeSpawnStandable is the 17-06 gate: for the default seed AND seed 25 the ported
// vanilla PlayerSpawnFinder (SpawnPos) returns a STANDABLE fresh-spawn over the
// FULLY-DECORATED spawn chunk — the floor block under the feet is a non-fluid solid, and
// the player's feet + head cells are clear air. This is the property that was VIOLATED by
// the old terrain-only (8,8) read (which buried the player in water/decoration; see the
// before/after coords logged below).
func TestSafeSpawnStandable(t *testing.T) {
	// noiseGenSeed (default) + 25 have clear land at the (8,8) center; seed 2 has WATER at the
	// (8,8) origin column — the exact case the OLD terrain-only (8,8) read buried the player
	// UNDERWATER (feet inside water blocks) — but other columns in the SAME chunk are land, so
	// the whole-chunk scan finds standable ground (the tree-at-(8,8)/ocean-at-(8,8) resilience
	// that is the heart of the fix). All three must yield a STANDABLE spawn (non-fluid solid
	// floor, >=2 air above).
	for _, seed := range []int64{noiseGenSeed, 25, 2} {
		g := NewNoiseGenerator(seed, testSecs, testMinY)

		sp := g.SpawnPos(level.ChunkPos{0, 0})
		if !sp.Found {
			t.Fatalf("seed=%#x: SpawnPos found no standable column (origin chunk + spiral)", seed)
		}

		// Read the blocks from the chunk that actually CONTAINS the safe spawn column (for an
		// ocean-origin seed the spiral fallback lands in a neighbor chunk).
		spawnChunkPos := level.ChunkPos{int32(int(sp.X-0.5) >> 4), int32(int(sp.Z-0.5) >> 4)}
		ch := g.Generate(spawnChunkPos)
		lx, lz := localOf(sp.X), localOf(sp.Z)
		feetY := int(sp.Y)

		floor := g.blockStateAt(ch, lx, feetY-1, lz)
		feet := g.blockStateAt(ch, lx, feetY, lz)
		head := g.blockStateAt(ch, lx, feetY+1, lz)

		// Floor must be a real, non-fluid solid the player stands ON.
		if block.IsAir(floor) {
			t.Errorf("seed=%#x: safe spawn floor at y=%d is air %q — player floats", seed, feetY-1, stateNameOf(floor))
		}
		if isFluidState(floor) {
			t.Errorf("seed=%#x: safe spawn floor at y=%d is fluid %q — player spawns on water/lava", seed, feetY-1, stateNameOf(floor))
		}
		// Feet + head (>=2 air blocks above the floor) must be clear so the player is not
		// embedded in terrain or decoration.
		if !block.IsAir(feet) {
			t.Errorf("seed=%#x: safe spawn feet at y=%d is %q (non-air) — player embedded", seed, feetY, stateNameOf(feet))
		}
		if !block.IsAir(head) {
			t.Errorf("seed=%#x: safe spawn head at y=%d is %q (non-air) — player embedded", seed, feetY+1, stateNameOf(head))
		}

		// Before/after evidence: what the OLD terrain-only (8,8) + surfaceY+2 placement put the
		// player in, on the ORIGIN decorated chunk. Logged so the fix's effect is auditable.
		originCh := g.Generate(level.ChunkPos{0, 0})
		tch := g.GenerateTerrain(level.ChunkPos{0, 0})
		const sx, sz = 8, 8
		col := sz<<4 | sx
		oldFeetY := (tch.HeightMaps.WorldSurface.Get(col) + g.minY - 1) + 2
		oldFeet := g.blockStateAt(originCh, sx, oldFeetY, sz)
		t.Logf("seed=%#x: OLD (8,8) feet=(%d,%d,%d) in %q  ->  NEW safe feet=(%.1f,%.1f,%.1f) on %q",
			seed, sx, oldFeetY, sz, stateNameOf(oldFeet), sp.X, sp.Y, sp.Z, stateNameOf(floor))
	}
}

// TestSafeSpawnDeterministic asserts SpawnPos is PURE over (seed, pos): two independent
// generators built from the same seed return byte-identical safe spawns. This is the
// determinism contract the bootstrap relies on (the same player always spawns at the same
// place run-to-run) and that the PARITY determinism gates depend on.
func TestSafeSpawnDeterministic(t *testing.T) {
	for _, seed := range []int64{noiseGenSeed, 25, 2} {
		a := NewNoiseGenerator(seed, testSecs, testMinY).SpawnPos(level.ChunkPos{0, 0})
		b := NewNoiseGenerator(seed, testSecs, testMinY).SpawnPos(level.ChunkPos{0, 0})
		if a != b {
			t.Errorf("seed=%#x: SpawnPos not deterministic: %+v vs %+v", seed, a, b)
		}
		// And a second call on the SAME generator is stable too.
		g := NewNoiseGenerator(seed, testSecs, testMinY)
		c1 := g.SpawnPos(level.ChunkPos{0, 0})
		c2 := g.SpawnPos(level.ChunkPos{0, 0})
		if c1 != c2 {
			t.Errorf("seed=%#x: repeated SpawnPos on one generator differs: %+v vs %+v", seed, c1, c2)
		}
	}
}

// TestSafeSpawnSurfaceYConsistent asserts the retained scalar SpawnSurfaceY agrees with the
// safe finder: it returns the standable FLOOR block world-Y (one below the safe feet cell),
// so the tick's SetSpawn / the respawn "+2" math lands the player two air blocks above a
// real floor — never embedded.
func TestSafeSpawnSurfaceYConsistent(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	sp := g.SpawnPos(level.ChunkPos{0, 0})
	if !sp.Found {
		t.Skip("no safe spawn for this seed (void/ocean) — SpawnSurfaceY uses the terrain fallback")
	}
	wantFloorY := int(sp.Y) - 1
	if got := g.SpawnSurfaceY(level.ChunkPos{0, 0}); got != wantFloorY {
		t.Errorf("SpawnSurfaceY = %d, want %d (safe feet %.0f minus 1)", got, wantFloorY, sp.Y)
	}
}
