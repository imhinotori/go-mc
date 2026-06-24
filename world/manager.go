package world

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

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

// floorDiv16 is arithmetic floor-division by 16 (chunk width) that is correct for
// negative coordinates (Go's / truncates toward zero, which is wrong west/north of 0,
// e.g. -1/16 == 0 but the column is -1). A right-shift on a signed int is the
// arithmetic (sign-extending) shift, so this is the single negative-correct world
// block-x/z -> chunk-column mapping the block-edit API uses. The world package CANNOT
// import server (import cycle), so the same logic the server's floorDiv provides lives
// here locally — one mapping per package, not two competing definitions.
func floorDiv16(v int) int { return v >> 4 }

// sectionLocal maps a world block (x,y,z) plus the dimension floor minY to the section
// index and the in-section y-major local index. It MIRRORS world/generator.go's
// Superflat.sectionLocal EXACTLY (the single authoritative mapping): sec = (y-minY)>>4;
// local = (y&15)<<8 | (z&15)<<4 | (x&15). Go's >> on a signed int is arithmetic and y&15
// is correct for negatives (e.g. (-1)&15 == 15), so the bit-masked local is negative-safe
// without any extra correction; only the COLUMN needs the floor-div helper above.
func sectionLocal(x, y, z, minY int) (sec, local int) {
	sec = (y - minY) >> 4
	local = (y&15)<<8 | (z&15)<<4 | (x & 15)
	return
}

// columnAndSection resolves the ready chunk, section index, and local index for a world
// block pos, or ok=false when the column is not loaded/ready or the y falls outside the
// chunk's section range. It is the shared lookup behind GetBlock and SetBlock so the two
// can never drift in their pos->(column,section,local) mapping.
func (m *ChunkManager) columnAndSection(pos pk.Position, minY int) (ch *level.Chunk, sec, local int, ok bool) {
	col := level.ChunkPos{int32(floorDiv16(pos.X)), int32(floorDiv16(pos.Z))}
	ch, loaded := m.Get(col)
	if !loaded {
		return nil, 0, 0, false // unloaded/not-ready column: never mutate, never read
	}
	sec, local = sectionLocal(pos.X, pos.Y, pos.Z, minY)
	if sec < 0 || sec >= len(ch.Sections) {
		return nil, 0, 0, false // y outside the dimension's section range
	}
	return ch, sec, local, true
}

// GetBlock reads the block state at a world block pos via the generator's section/local
// mapping. ok is false when the target column is not loaded/ready or y is outside the
// section range (treated as "no block here" — physics reads it as non-solid air). Threaded
// the dimension floor minY (overworld -64) so the y->section mapping is correct. Pure read;
// runs on the tick goroutine over the tick-owned manager (TICK-05).
func (m *ChunkManager) GetBlock(pos pk.Position, minY int) (block.StateID, bool) {
	ch, sec, local, ok := m.columnAndSection(pos, minY)
	if !ok {
		return 0, false
	}
	return ch.Sections[sec].GetBlock(local), true
}

// SetBlock writes state at a world block pos and reports whether it CHANGED the world
// (false when the new state equals the existing one, or — the server-authoritative reject
// path — when the target column is not loaded/ready or y is out of range, in which case
// NOTHING is mutated and there is no panic). It delegates to level.Section.SetBlock, which
// maintains the section's non-air BlockCount, so the count never goes stale. minY is the
// dimension floor (overworld -64). The SOLE block mutator; runs on the tick goroutine over
// the tick-owned manager (TICK-05 / T-6-08) — no off-tick caller writes the world.
func (m *ChunkManager) SetBlock(pos pk.Position, state block.StateID, minY int) (changed bool) {
	ch, sec, local, ok := m.columnAndSection(pos, minY)
	if !ok {
		return false // unloaded column / out-of-range y: no mutation (server-authoritative reject)
	}
	old := ch.Sections[sec].GetBlock(local)
	if old == state {
		return false // already this state: a no-op, do not re-broadcast an unchanged edit
	}
	ch.Sections[sec].SetBlock(local, state)
	return true
}
