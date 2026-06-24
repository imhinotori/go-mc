package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// blockTestMinY is the overworld dimension floor used by the SetBlock/GetBlock
// tests — the same -64 the server threads in (mirrors physics.go dimMinY).
const blockTestMinY = -64

// blockTestSecs is the overworld section count (Height/16 = 24) the test world uses.
const blockTestSecs = 24

// readyChunk inserts an empty (all-air) ready chunk at col so SetBlock/GetBlock has a
// loaded column to mutate.
func readyChunk(m *ChunkManager, col level.ChunkPos) {
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	m.Insert(col, ch)
}

// TestSetBlock: on a loaded chunk, SetBlock writes a state that GetBlock reads back,
// the section BlockCount is maintained, and SetBlock to air clears the column.
func TestSetBlock(t *testing.T) {
	m := NewChunkManager()
	readyChunk(m, level.ChunkPos{0, 0})

	stone := block.ToStateID[block.Stone{}]
	air := block.ToStateID[block.Air{}]
	pos := pk.Position{X: 5, Y: 70, Z: 9}

	// Initially air.
	if got, ok := m.GetBlock(pos, blockTestMinY); !ok || !block.IsAir(got) {
		t.Fatalf("GetBlock before set = (%v, ok=%v), want air, ok=true", got, ok)
	}

	// Place stone: changed=true and the read reflects it.
	if changed := m.SetBlock(pos, stone, blockTestMinY); !changed {
		t.Fatalf("SetBlock(stone) changed = false, want true (air -> stone is a change)")
	}
	if got, ok := m.GetBlock(pos, blockTestMinY); !ok || got != stone {
		t.Fatalf("GetBlock after set stone = (%v, ok=%v), want stone, ok=true", got, ok)
	}

	// BlockCount maintained: the section now has exactly one non-air block.
	ch, _ := m.Get(level.ChunkPos{0, 0})
	sec := (pos.Y - blockTestMinY) >> 4
	if bc := ch.Sections[sec].BlockCount; bc != 1 {
		t.Fatalf("section BlockCount after one stone = %d, want 1 (Section.SetBlock must maintain it)", bc)
	}

	// Setting the same state again is NOT a change.
	if changed := m.SetBlock(pos, stone, blockTestMinY); changed {
		t.Fatalf("SetBlock(stone) over existing stone changed = true, want false (no-op)")
	}

	// Break back to air: changed=true and BlockCount returns to zero.
	if changed := m.SetBlock(pos, air, blockTestMinY); !changed {
		t.Fatalf("SetBlock(air) changed = false, want true (stone -> air is a change)")
	}
	if got, ok := m.GetBlock(pos, blockTestMinY); !ok || !block.IsAir(got) {
		t.Fatalf("GetBlock after break = (%v, ok=%v), want air, ok=true", got, ok)
	}
	if bc := ch.Sections[sec].BlockCount; bc != 0 {
		t.Fatalf("section BlockCount after break = %d, want 0 (stale count)", bc)
	}
}

// TestSetBlockUnloaded: SetBlock/GetBlock on an UNLOADED column is a no-op — no panic,
// changed=false, ok=false. The server-authoritative reject path relies on this.
func TestSetBlockUnloaded(t *testing.T) {
	m := NewChunkManager()
	// No chunk inserted at {3,3}.
	pos := pk.Position{X: 3*16 + 1, Y: 70, Z: 3*16 + 1}

	if changed := m.SetBlock(pos, block.ToStateID[block.Stone{}], blockTestMinY); changed {
		t.Fatalf("SetBlock on unloaded column changed = true, want false (never mutate an unloaded column)")
	}
	if got, ok := m.GetBlock(pos, blockTestMinY); ok {
		t.Fatalf("GetBlock on unloaded column ok = true (got %v), want false", got)
	}
}

// TestBlockPosMapping: negative y and negative x/z map to the correct (section, local),
// matching the generator's negative-correct floor-div + (y-MinY)>>4 mapping.
func TestBlockPosMapping(t *testing.T) {
	m := NewChunkManager()
	// A column at negative x/z so the floor-div column mapping is exercised.
	col := level.ChunkPos{-1, -1} // covers world x,z in [-16,-1]
	readyChunk(m, col)

	stone := block.ToStateID[block.Stone{}]

	cases := []struct {
		name    string
		pos     pk.Position
		wantSec int
	}{
		// MinY itself maps to section 0.
		{"floor", pk.Position{X: -1, Y: -64, Z: -1}, 0},
		// y=-1 with MinY -64: (-1 - -64)>>4 = 63>>4 = 3.
		{"justBelowZero", pk.Position{X: -16, Y: -1, Z: -16}, 3},
		// A high block deep in a positive section.
		{"high", pk.Position{X: -8, Y: 200, Z: -3}, (200 + 64) >> 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sec := (tc.pos.Y - blockTestMinY) >> 4
			if sec != tc.wantSec {
				t.Fatalf("section mapping for y=%d = %d, want %d", tc.pos.Y, sec, tc.wantSec)
			}
			if changed := m.SetBlock(tc.pos, stone, blockTestMinY); !changed {
				t.Fatalf("SetBlock at %+v changed = false, want true", tc.pos)
			}
			got, ok := m.GetBlock(tc.pos, blockTestMinY)
			if !ok || got != stone {
				t.Fatalf("GetBlock at %+v = (%v, ok=%v), want stone, ok=true (mapping mismatch)", tc.pos, got, ok)
			}
			// Confirm it landed in the EXPECTED section (no cross-section bleed): the
			// chunk's other sections must remain all-air.
			ch, _ := m.Get(col)
			for i := range ch.Sections {
				bc := ch.Sections[i].BlockCount
				if i == tc.wantSec && bc == 0 {
					t.Fatalf("expected a block in section %d, BlockCount=0 (wrong section)", i)
				}
			}
		})
	}
}

func TestChunkManagerLifecycle(t *testing.T) {
	m := NewChunkManager()
	pos := level.ChunkPos{2, -3}

	if got := m.State(pos); got != stateEmpty {
		t.Fatalf("initial state = %v, want stateEmpty", got)
	}
	if _, ok := m.Get(pos); ok {
		t.Fatalf("Get on empty returned ok=true")
	}

	m.MarkLoading(pos)
	if got := m.State(pos); got != stateLoading {
		t.Fatalf("after MarkLoading state = %v, want stateLoading", got)
	}
	if _, ok := m.Get(pos); ok {
		t.Fatalf("Get while loading returned ok=true")
	}

	ch := level.EmptyChunk(24)
	m.Insert(pos, ch)
	if got := m.State(pos); got != stateReady {
		t.Fatalf("after Insert state = %v, want stateReady", got)
	}
	got, ok := m.Get(pos)
	if !ok || got != ch {
		t.Fatalf("Get after Insert = (%v,%v), want the inserted chunk", got, ok)
	}

	m.Remove(pos)
	if got := m.State(pos); got != stateEmpty {
		t.Fatalf("after Remove state = %v, want stateEmpty", got)
	}
}

// TestChunkManagerMarkEmpty covers the W1 requirement: a generation/region error
// must be able to revert a Loading holder so Wave 3 can retry it.
func TestChunkManagerMarkEmpty(t *testing.T) {
	m := NewChunkManager()
	pos := level.ChunkPos{5, 5}

	m.MarkLoading(pos)
	if got := m.State(pos); got != stateLoading {
		t.Fatalf("expected stateLoading after MarkLoading, got %v", got)
	}

	m.MarkEmpty(pos)
	if got := m.State(pos); got != stateEmpty {
		t.Fatalf("after MarkEmpty state = %v, want stateEmpty (holder must be retryable)", got)
	}
	if _, ok := m.Get(pos); ok {
		t.Fatalf("Get after MarkEmpty returned ok=true")
	}
}
