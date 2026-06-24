package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

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
