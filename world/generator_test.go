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

	// Real light (LevelLightEngine) now computes sky light instead of the old fullSkyLight seal.
	// The superflat has solid stone up to SurfaceY (y=-1) and open air above; every AIR section
	// above the surface is fully sky-lit (carries a 2048-byte 0xFF array), and the open-air cell
	// directly above the surface reads sky level 15. Sections that are entirely inside the solid
	// stone are dark (no sky reaches them). CITE: SkyLightEngine.propagateLightSources.
	surfaceSec, _ := sectionIndex(testMinY, 0, testSurfaceY, 0)
	for i := surfaceSec + 1; i < len(ch.Sections); i++ {
		if len(ch.Sections[i].SkyLight) != 2048 {
			t.Fatalf("above-surface section %d SkyLight len = %d, want 2048 (fully lit)", i, len(ch.Sections[i].SkyLight))
		}
	}
	// the air cell at y = SurfaceY+1 must be full sky light (15).
	aboveSec, aboveIdx := sectionIndex(testMinY, 0, testSurfaceY+1, 0)
	sl := ch.Sections[aboveSec].SkyLight
	if len(sl) != 2048 {
		t.Fatalf("section above surface has no sky light array")
	}
	nib := int(sl[aboveIdx>>1] >> (4 * (aboveIdx & 1)) & 0xF)
	if nib != 15 {
		t.Fatalf("sky light at open-air cell above surface = %d, want 15", nib)
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
