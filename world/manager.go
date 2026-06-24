package world

import "github.com/imhinotori/sulfur/level"

// loadState is the per-holder load lifecycle. The tick goroutine (Plan 04-03)
// drives the transitions: Empty -> Loading (request issued) -> Ready (worker
// result applied), or Loading -> Empty on a generation/region error (retry).
type loadState int

const (
	stateEmpty loadState = iota
	stateLoading
	stateReady
)

func (s loadState) String() string {
	switch s {
	case stateLoading:
		return "loading"
	case stateReady:
		return "ready"
	default:
		return "empty"
	}
}

type holder struct {
	state loadState
	chunk *level.Chunk // non-nil only when state == stateReady
}

// ChunkManager is a plain map of column position -> holder. It is OWNED by the
// tick goroutine (Plan 04-03) and is intentionally NOT goroutine-safe — the
// single-owner discipline (the same as the players map) is what keeps the
// off-tick rejoin -race clean. The needed-ring computation and per-player
// send-tracking live in the server package; this type is pure storage/query.
type ChunkManager struct {
	columns map[level.ChunkPos]*holder
}

func NewChunkManager() *ChunkManager {
	return &ChunkManager{columns: make(map[level.ChunkPos]*holder)}
}

// Get returns the ready chunk for pos, or (nil,false) if absent or not yet ready.
func (m *ChunkManager) Get(pos level.ChunkPos) (*level.Chunk, bool) {
	h := m.columns[pos]
	if h == nil || h.state != stateReady {
		return nil, false
	}
	return h.chunk, true
}

// State reports the load state of pos (stateEmpty if absent).
func (m *ChunkManager) State(pos level.ChunkPos) loadState {
	h := m.columns[pos]
	if h == nil {
		return stateEmpty
	}
	return h.state
}

// IsEmpty reports whether pos is unrequested (no holder, or a holder still in the
// Empty state). The streamer (Plan 04-03) uses it to decide whether to issue exactly
// one worker request per column — an exported predicate so the loadState enum stays
// package-private (the tick never needs the Loading/Ready distinction here, only
// "should I request this column").
func (m *ChunkManager) IsEmpty(pos level.ChunkPos) bool {
	h := m.columns[pos]
	return h == nil || h.state == stateEmpty
}

// MarkLoading transitions Empty -> Loading so the tick issues exactly one
// worker request for pos. A no-op if pos is already Loading or Ready.
func (m *ChunkManager) MarkLoading(pos level.ChunkPos) {
	if h := m.columns[pos]; h != nil {
		if h.state == stateEmpty {
			h.state = stateLoading
		}
		return
	}
	m.columns[pos] = &holder{state: stateLoading}
}

// Insert transitions a holder to Ready with the worker-produced chunk
// (Loading -> Ready). Called by the rejoin in Plan 04-03. Creates the holder if
// missing so a result is never lost.
func (m *ChunkManager) Insert(pos level.ChunkPos, ch *level.Chunk) {
	h := m.columns[pos]
	if h == nil {
		h = &holder{}
		m.columns[pos] = h
	}
	h.state = stateReady
	h.chunk = ch
}

// MarkEmpty reverts pos to Empty (drops any holder). Plan-check W1: a
// generation/region ERROR must be able to un-strand a Loading holder so the
// tick (Plan 04-03) can retry it on a later tick instead of leaving it stuck in
// Loading forever. Equivalent to Remove but named for the error path.
func (m *ChunkManager) MarkEmpty(pos level.ChunkPos) {
	delete(m.columns, pos)
}

// Remove unloads pos entirely.
func (m *ChunkManager) Remove(pos level.ChunkPos) {
	delete(m.columns, pos)
}

// Len reports the number of tracked columns (any state). The streamer uses it to
// assert the needed-ring stays bounded under position spam (threat T-4-06); it is a
// pure read over the tick-owned map.
func (m *ChunkManager) Len() int { return len(m.columns) }
