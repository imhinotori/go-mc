package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// overworld profile used across tests: MinY=-64, Height=384 -> secs=24.
const (
	testSecs     = 24
	testMinY     = -64
	testSurfaceY = testMinY + 4*16 - 1 // 4 sections of solid fill, top solid block at y=-1
)

func TestSuperflatSectionCount(t *testing.T) {
	g := NewSuperflat(testSecs, testMinY, testSurfaceY)
	ch := g.Generate(level.ChunkPos{0, 0})

	if got := len(ch.Sections); got != testSecs {
		t.Fatalf("section count = %d, want %d", got, testSecs)
	}
	if ch.Status != level.StatusFull {
		t.Fatalf("status = %q, want %q", ch.Status, level.StatusFull)
	}

	// Spot-check a column: bedrock at the bottom, stone up to SurfaceY, air above.
	bedrock := level.BlocksState(blockState(t))
	_ = bedrock

	// Bottom block (y == MinY) must be bedrock.
	secBottom, idxBottom := sectionIndex(testMinY, 0, testMinY, 0)
	if got := ch.Sections[secBottom].GetBlock(idxBottom); got == airState(t) {
		t.Fatalf("bottom block at y=%d is air, want bedrock", testMinY)
	}

	// Block at SurfaceY must be solid (stone).
	secSurf, idxSurf := sectionIndex(testMinY, 0, testSurfaceY, 0)
	if got := ch.Sections[secSurf].GetBlock(idxSurf); got == airState(t) {
		t.Fatalf("block at SurfaceY=%d is air, want stone", testSurfaceY)
	}

	// Block above SurfaceY must be air.
	secAir, idxAir := sectionIndex(testMinY, 0, testSurfaceY+1, 0)
	if got := ch.Sections[secAir].GetBlock(idxAir); got != airState(t) {
		t.Fatalf("block above SurfaceY (y=%d) is not air", testSurfaceY+1)
	}

	// FluidCount must be 0 on every section (exercises the 04-01 two-short layout).
	for i, s := range ch.Sections {
		if s.FluidCount != 0 {
			t.Fatalf("section %d FluidCount = %d, want 0", i, s.FluidCount)
		}
	}

	// WorldSurface heightmap should report SurfaceY-relative height for a sampled column.
	// Convention (documented in generator): heightmap value = (SurfaceY - MinY) + 1.
	wantH := (testSurfaceY - testMinY) + 1
	if got := ch.HeightMaps.WorldSurface.Get(0); got != wantH {
		t.Fatalf("WorldSurface heightmap[0] = %d, want %d", got, wantH)
	}

	// SkyLight must be present (full 2048 bytes) on every section.
	for i, s := range ch.Sections {
		if len(s.SkyLight) != 2048 {
			t.Fatalf("section %d SkyLight len = %d, want 2048", i, len(s.SkyLight))
		}
	}
}

func TestSuperflatDeterministic(t *testing.T) {
	g := NewSuperflat(testSecs, testMinY, testSurfaceY)

	pos := level.ChunkPos{3, 7}
	var a, b bytes.Buffer
	if _, err := g.Generate(pos).WriteTo(&a); err != nil {
		t.Fatalf("first WriteTo: %v", err)
	}
	if _, err := g.Generate(pos).WriteTo(&b); err != nil {
		t.Fatalf("second WriteTo: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("Generate(%v) is not deterministic: %d vs %d bytes differ", pos, a.Len(), b.Len())
	}
}

// helpers ------------------------------------------------------------------

// sectionIndex maps a world (x,y,z) to its section index and the in-section
// local index, using MinY as the world floor.
func sectionIndex(minY, x, y, z int) (sec, local int) {
	sec = (y - minY) >> 4
	local = (y&15)<<8 | (z&15)<<4 | (x & 15)
	return
}

func airState(t *testing.T) level.BlocksState {
	t.Helper()
	// A freshly EmptyChunk section reports air via GetBlock; reuse a fresh chunk.
	ch := level.EmptyChunk(1)
	return ch.Sections[0].GetBlock(0)
}

func blockState(t *testing.T) level.BlocksState {
	t.Helper()
	return airState(t)
}
