package level

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/save"
)

// TestChunkWireRoundTripAfterEdit reproduces BUG-5: a chunk that has been EDITED (a player place/
// break growing a section's palette) must survive the full persist round-trip AND re-encode to a
// network-valid LevelChunkWithLight body — i.e. ChunkToSave -> ChunkFromSave -> WriteTo must
// produce section data the client (and our symmetric ReadFrom) can decode without an index/size
// fault. The client crash on respawn was an IndexOutOfBounds inside PalettedContainer.read, which
// means a section's (bits, palette, long-array) triple was inconsistent on the wire after reload.
func TestChunkWireRoundTripAfterEdit(t *testing.T) {
	const secs = 24
	src := EmptyChunk(secs)
	src.Status = StatusFull

	// Edit MANY distinct block states into one section so its palette grows past every
	// boundary (single -> linear(4) -> hash(5..8) -> global). This is the player-edit path
	// (PaletteContainer.Set resize) the generated-only round-trip never exercised.
	sec := &src.Sections[5]
	nStates := len(block.StateList)
	count := 600
	if count > nStates {
		count = nStates - 1
	}
	for i := 0; i < count; i++ {
		// distinct REAL state ids across the section so the palette grows past every boundary
		// (single -> linear -> hash -> global) while countNoneAirBlocks can validate each id.
		sec.States.Set(i, BlocksState(i+1))
	}
	sec.BlockCount = countNoneAirBlocks(sec)

	// Persist round-trip: ChunkToSave -> ChunkFromSave (the worker.decodeAndSeed path) — the
	// exact path that crashed on respawn. Before the fix this produced a section with a 0-length
	// Anvil palette + a direct-id data array; on reload the states read back out of range.
	var sc save.Chunk
	if err := ChunkToSave(src, &sc); err != nil {
		t.Fatalf("ChunkToSave: %v", err)
	}
	// The edited section's Anvil palette must be EXPLICIT (non-zero) with a matching data array —
	// the global/direct format is illegal in Anvil and is what corrupted the reload.
	// count edited states + 1 (the default air filling cells count..4095) = count+1 distinct.
	if pl := len(sc.Sections[5].BlockStates.Palette); pl != count+1 {
		t.Fatalf("edited section saved with palette len %d, want %d (global-palette corruption)", pl, count+1)
	}
	reloaded, err := ChunkFromSave(&sc)
	if err != nil {
		t.Fatalf("ChunkFromSave: %v", err)
	}

	// Every reloaded state id must be in range (the client's PalettedContainer.read precondition);
	// an out-of-range id is exactly the IndexOutOfBounds the client hit.
	for i := 0; i < count; i++ {
		got := reloaded.Sections[5].States.Get(i)
		if int(got) < 0 || int(got) >= nStates {
			t.Fatalf("reloaded section cell %d -> OUT OF RANGE state id %d (corrupt section)", i, got)
		}
		if got != BlocksState(i+1) {
			t.Fatalf("section state mismatch at %d after round-trip: got %d want %d", i, got, i+1)
		}
	}

	// The reloaded >256-entry section must use the GLOBAL/direct in-memory representation
	// generation produces (so the network WriteTo emits the direct format the 26.2 client expects
	// at bits>=9 — NOT an indexed palette the client would misread).
	if _, ok := reloaded.Sections[5].States.palette.(*globalPalette[BlocksState]); !ok {
		t.Fatalf("reloaded >256-entry section is %T, want *globalPalette (direct network format)", reloaded.Sections[5].States.palette)
	}

	// Network round-trip (WriteLevelChunkWithLight body = ch.WriteTo, then the client's
	// PalettedContainer.read via the symmetric ReadFrom) must preserve every edited state.
	var body bytes.Buffer
	if _, err := reloaded.WriteTo(&body); err != nil {
		t.Fatalf("reloaded WriteTo: %v", err)
	}
	dst := EmptyChunk(secs)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("client-side chunk decode PANICKED (the IndexOutOfBounds): %v", r)
			}
		}()
		if _, err := dst.ReadFrom(bytes.NewReader(body.Bytes())); err != nil {
			t.Fatalf("client-side chunk decode failed: %v", err)
		}
	}()
	for i := 0; i < count; i++ {
		if got := dst.Sections[5].States.Get(i); got != BlocksState(i+1) {
			t.Fatalf("network round-trip state mismatch at %d: got %d want %d", i, got, i+1)
		}
	}
}
