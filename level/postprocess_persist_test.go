package level

import (
	"testing"

	"github.com/imhinotori/sulfur/save"
)

// TestPostProcessFluidsPersistRoundTrip locks the persistence of the aquifer/carver fluid marks
// (Chunk.PostProcessFluids): they must survive ChunkToSave -> ChunkFromSave via the vanilla
// "PostProcessing" ListTag (one per-section ShortList). Without this the gen-time marks are lost on
// the first save, so a reloaded chunk's cave/ravine water never flows on revisit (the operator's
// "el agua sigue pegada ... puede ser por el mundo viejo guardado").
func TestPostProcessFluidsPersistRoundTrip(t *testing.T) {
	src := EmptyChunk(24)
	src.Status = StatusFull
	// A spread of marks across sections: full local Y = worldY - minY (minY = -64).
	src.PostProcessFluids = []uint32{
		uint32(107)<<8 | uint32(13)<<4 | uint32(15), // section 6, in-sec Y 11
		uint32(200)<<8 | uint32(2)<<4 | uint32(3),   // section 12
		uint32(50)<<8 | uint32(0)<<4 | uint32(0),    // section 3
		uint32(107)<<8 | uint32(0)<<4 | uint32(1),   // same section as the first
	}

	var sc save.Chunk
	sc.YPos = -4 // minY -64 / 16
	if err := ChunkToSave(src, &sc); err != nil {
		t.Fatalf("ChunkToSave: %v", err)
	}
	if sc.PostProcessing.Type == 0 {
		t.Fatal("PostProcessing tag not written")
	}

	back, err := ChunkFromSave(&sc)
	if err != nil {
		t.Fatalf("ChunkFromSave: %v", err)
	}

	want := map[uint32]bool{}
	for _, v := range src.PostProcessFluids {
		want[v] = true
	}
	if len(back.PostProcessFluids) != len(src.PostProcessFluids) {
		t.Fatalf("PostProcessFluids count %d after round-trip, want %d", len(back.PostProcessFluids), len(src.PostProcessFluids))
	}
	for _, v := range back.PostProcessFluids {
		if !want[v] {
			t.Errorf("unexpected cell %#x after round-trip", v)
		}
		delete(want, v)
	}
	if len(want) != 0 {
		t.Errorf("missing cells after round-trip: %v", want)
	}
}
