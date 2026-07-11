package world

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
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
	// loadTick is the manager tick at which this holder entered stateLoading. RetryStale reverts a
	// holder Loading too long back to Empty so the streamer re-requests it — recovery for a worker
	// request DROPPED under burst backpressure (worker.Request is non-blocking and silently drops
	// when its bounded queue is full). Without it a dropped request leaves the column Loading forever
	// (IsEmpty=false → never re-requested → the chunk renders transparent permanently).
	loadTick int64
}

// ChunkManager is a plain map of column position -> holder. It is OWNED by the
// tick goroutine (Plan 04-03) and is intentionally NOT goroutine-safe — the
// single-owner discipline (the same as the players map) is what keeps the
// off-tick rejoin -race clean. The needed-ring computation and per-player
// send-tracking live in the server package; this type is pure storage/query.
type ChunkManager struct {
	columns map[level.ChunkPos]*holder
	// dirty is the SUB-PERSIST dirty-chunk set: a column is marked dirty the moment its
	// blocks/block-entities change (SetBlock, or an explicit MarkDirty for BE/entity edits),
	// and cleared when the save loop has snapshotted it (DrainDirty). It is part of the SAME
	// tick-owned single-owner discipline as columns — touched ONLY on the tick goroutine
	// (TICK-05), so it needs no lock. A dirty column that is later Remove'd (unload) is dropped
	// from the set too, so an unloaded chunk is not spuriously re-saved from a stale flag.
	dirty map[level.ChunkPos]struct{}
	// nowTick is a monotonic counter the streamer advances once per tick (Tick) so RetryStale can
	// measure how long a column has been Loading.
	nowTick int64
}

func NewChunkManager() *ChunkManager {
	return &ChunkManager{
		columns: make(map[level.ChunkPos]*holder),
		dirty:   make(map[level.ChunkPos]struct{}),
	}
}

// Tick advances the manager's monotonic tick counter (once per server tick) for RetryStale.
func (m *ChunkManager) Tick() { m.nowTick++ }

// RetryStale reverts to Empty every column stuck in stateLoading longer than graceTicks (a request
// dropped under burst backpressure). Reverting lets the next tickChunks re-issue the worker request.
// Ready columns are never touched. Returns the count reverted. Tick-owned.
func (m *ChunkManager) RetryStale(graceTicks int64) int {
	n := 0
	for _, h := range m.columns {
		if h.state == stateLoading && m.nowTick-h.loadTick > graceTicks {
			h.state = stateEmpty
			n++
		}
	}
	return n
}

// MarkDirty flags pos as needing a save (SUB-PERSIST). The tick calls it for any chunk mutation
// the block path does not itself capture — block-entity edits (a chest's items changing), entity
// spawns/despawns persisted into the column, etc. SetBlock marks dirty itself, so a plain block
// edit need not call this. A no-op for an unloaded/absent column (a chunk that is not Ready has
// nothing to serialize). Tick-owned (TICK-05).
func (m *ChunkManager) MarkDirty(pos level.ChunkPos) {
	if h := m.columns[pos]; h == nil || h.state != stateReady {
		return // not a live column: nothing to save
	}
	m.dirty[pos] = struct{}{}
}

// IsDirty reports whether pos is currently flagged dirty. A pure read for the save scheduler /
// tests. Tick-owned.
func (m *ChunkManager) IsDirty(pos level.ChunkPos) bool {
	_, ok := m.dirty[pos]
	return ok
}

// DrainDirty returns the current dirty columns (as a fresh slice) and CLEARS the dirty set, so a
// chunk is snapshotted at most once per drain and a no-further-change chunk is not re-saved. The
// caller (the tick's save phase) snapshots each returned column on the OWNER goroutine and hands
// the immutable bytes off-tick — exactly the leaveSnapshots discipline. Returns a nil slice when
// nothing is dirty (the common idle case). Tick-owned (TICK-05).
func (m *ChunkManager) DrainDirty() []level.ChunkPos {
	if len(m.dirty) == 0 {
		return nil
	}
	out := make([]level.ChunkPos, 0, len(m.dirty))
	for pos := range m.dirty {
		out = append(out, pos)
	}
	// Clear the set in place (a fresh map keeps the allocation small after a burst).
	m.dirty = make(map[level.ChunkPos]struct{})
	return out
}

// Get returns the ready chunk for pos, or (nil,false) if absent or not yet ready.
func (m *ChunkManager) Get(pos level.ChunkPos) (*level.Chunk, bool) {
	h := m.columns[pos]
	if h == nil || h.state != stateReady {
		return nil, false
	}
	return h.chunk, true
}

// ForEachReady invokes fn for every column that is Ready (loaded, holding a chunk). It is the
// enumeration seam the random-tick driver needs — the vanilla ServerChunkCache.tickChunks path
// iterates the loaded/block-ticking chunks (chunkMap.forEachBlockTickingChunk) and calls
// ServerLevel.tickChunk on each. This exposes that loaded-column set WITHOUT leaking the private
// holder map. fn MUST NOT structurally mutate the column map (Insert/Remove/MarkLoading) during
// iteration — the driver only reads chunk sections and writes block STATE via SetBlock (which
// mutates a section in place, never the column map), so ranging is safe. Tick-owned; runs on the
// owner goroutine over the tick-owned manager (TICK-05). CITE: ServerChunkCache.tickChunks ->
// chunkMap.forEachBlockTickingChunk(chunk -> level.tickChunk(chunk, tickSpeed)).
func (m *ChunkManager) ForEachReady(fn func(pos level.ChunkPos, ch *level.Chunk)) {
	for pos, h := range m.columns {
		if h.state == stateReady && h.chunk != nil {
			fn(pos, h.chunk)
		}
	}
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
			h.loadTick = m.nowTick
		}
		return
	}
	m.columns[pos] = &holder{state: stateLoading, loadTick: m.nowTick}
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
	delete(m.dirty, pos) // a dropped column has nothing to save (no stale-flag re-save)
}

// Remove unloads pos entirely. The caller (the save scheduler) is responsible for FLUSHING a
// dirty column BEFORE Remove (the on-unload save) — Remove itself just drops the holder + clears
// the dirty flag so an unloaded chunk is never re-saved from a stale flag.
func (m *ChunkManager) Remove(pos level.ChunkPos) {
	delete(m.columns, pos)
	delete(m.dirty, pos)
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

// BiomeAt reads the biome at a world block pos from the loaded chunk's per-section biome
// container, or ok=false when the column is not loaded/ready or y is outside the section
// range. It mirrors vanilla ServerLevel.getBiome, which resolves the biome from the loaded
// chunk's biome storage (LevelChunk.getNoiseBiome) at QUART resolution: a section holds a
// 4x4x4 biome grid, so the block coords are converted to quart cells (x&15)>>2 etc. and
// indexed y-major exactly as world/levelgen/surface.FillBiomes writes them --
// ((y&15>>2)*4 + (z&15>>2))*4 + (x&15>>2) -- so a read here returns the identical Type the
// generator stored. Pure read over the tick-owned manager (TICK-05), the biome-lookup seam
// the NaturalSpawner weighted mob pick (server/natural_spawner.go getRandomSpawnMobAt) needs.
func (m *ChunkManager) BiomeAt(pos pk.Position, minY int) (level.BiomesState, bool) {
	ch, sec, _, ok := m.columnAndSection(pos, minY)
	if !ok {
		return 0, false
	}
	bx := (pos.X & 15) >> 2
	by := (pos.Y & 15) >> 2
	bz := (pos.Z & 15) >> 2
	idx := (by*4+bz)*4 + bx
	return ch.Sections[sec].Biomes.Get(idx), true
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
	// LevelChunk.setBlockState updates the four live heightmaps (MOTION_BLOCKING,
	// MOTION_BLOCKING_NO_LEAVES, OCEAN_FLOOR, WORLD_SURFACE) on every block set so they never
	// go stale after a player build/break. Local column coords are pos&15; Y is absolute.
	// CITE: LevelChunk.setBlockState -> Heightmap.update x4.
	ch.UpdateHeightmaps(pos.X&15, pos.Y, pos.Z&15, minY, state)
	// SUB-PERSIST: a CHANGED block dirties its column so the save loop flushes it. Marked here,
	// at the SOLE block mutator, so every block edit (place/break/fluid/vegetation) is captured
	// without each call site remembering to mark. The column is Ready (columnAndSection resolved
	// it), so MarkDirty is never a no-op here. Tick-owned (SetBlock runs only on the owner).
	col := level.ChunkPos{int32(floorDiv16(pos.X)), int32(floorDiv16(pos.Z))}
	m.dirty[col] = struct{}{}
	return true
}

// SetBlockEntityAt creates or replaces a block entity at the world pos in its owning chunk's
// BlockEntity list — the runtime equivalent of LevelChunk.setBlockState's hasBlockEntity() branch
// (newBlockEntity + addAndRegisterBlockEntity), which vanilla runs synchronously when a block with an
// EntityBlock is placed. Sulfur's place path writes only the block state via SetBlock, so a placed
// chest had no BlockEntity and never opened; calling this after SetBlock for a block-entity block
// (e.g. chest) records the empty BE so the open path resolves it. data is the bare compound payload
// (empty for a freshly-placed chest -> no LootTable -> opens empty). Marks the column dirty so the
// BE persists. A no-op for an unloaded column. Tick-owned (TICK-05). CITE: LevelChunk.setBlockState
// -> EntityBlock.newBlockEntity -> addAndRegisterBlockEntity.
func (m *ChunkManager) SetBlockEntityAt(pos pk.Position, typ block.EntityType, data nbt.RawMessage, minY int) bool {
	col := level.ChunkPos{int32(floorDiv16(pos.X)), int32(floorDiv16(pos.Z))}
	ch, ok := m.Get(col)
	if !ok {
		return false
	}
	lx, lz := pos.X&15, pos.Z&15
	be := level.BlockEntity{Y: int16(pos.Y), Type: typ, Data: data}
	if !be.PackXZ(lx, lz) {
		return false
	}
	// Replace any existing BE at this exact cell (re-place over an old one), else append.
	for i := range ch.BlockEntity {
		bx, bz := ch.BlockEntity[i].UnpackXZ()
		if bx == lx && bz == lz && int(ch.BlockEntity[i].Y) == pos.Y {
			ch.BlockEntity[i] = be
			m.dirty[col] = struct{}{}
			return true
		}
	}
	ch.BlockEntity = append(ch.BlockEntity, be)
	m.dirty[col] = struct{}{}
	return true
}

// RemoveBlockEntityAt drops any block entity recorded at the world pos (used when a block-entity
// block is broken, so a stale BE never lingers). A no-op when none exists / column unloaded.
// Tick-owned. CITE: LevelChunk.setBlockState removeBlockEntity on a state losing its BlockEntity.
func (m *ChunkManager) RemoveBlockEntityAt(pos pk.Position) bool {
	col := level.ChunkPos{int32(floorDiv16(pos.X)), int32(floorDiv16(pos.Z))}
	ch, ok := m.Get(col)
	if !ok {
		return false
	}
	lx, lz := pos.X&15, pos.Z&15
	for i := range ch.BlockEntity {
		bx, bz := ch.BlockEntity[i].UnpackXZ()
		if bx == lx && bz == lz && int(ch.BlockEntity[i].Y) == pos.Y {
			ch.BlockEntity = append(ch.BlockEntity[:i], ch.BlockEntity[i+1:]...)
			m.dirty[col] = struct{}{}
			return true
		}
	}
	return false
}
