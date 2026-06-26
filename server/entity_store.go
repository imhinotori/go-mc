package server

import (
	"math"
	"sort"
	"sync/atomic"

	"github.com/imhinotori/sulfur/level"
)

// entity_store.go is the ENT-01 foundation: the monotonic entity-ID allocator and the
// tick-owned entity collection with per-chunk-section grid bucketing for the tracker's
// broad phase (Plan 06-02). It is the substrate every other Phase-6 slice references.

// EntityIDAllocator issues a unique, never-reused, monotonically increasing int32 id for
// every player AND every entity. It is the replacement for the old hard-coded
// joinEntityID=1 (06-RESEARCH Pitfall 7): players and entities now draw from ONE id space
// so a spawned entity can never collide a player's id (threat T-6-07).
//
// The counter is an atomic.Int32 claimed exactly like gameTick.teleportSeq: AcceptPlayer
// runs on a per-connection accept goroutine and claims a player's id off-tick, while the
// tick goroutine also claims ids when it spawns entities — both go through the same atomic
// Add, which crosses NO tick-owned game state. So the allocator stays -race clean BY
// CONSTRUCTION even though it is read from two goroutines (T-6-08, proven by the Docker
// -race gate). It is a tick-owned FIELD on TickLoop, but the only state it carries is the
// atomic counter, so the off-tick claim is safe.
type EntityIDAllocator struct {
	// next is the last-issued id. AllocID pre-increments (Add(1)), so the first issued id is
	// 1 (deterministic, never 0 — distinguishing "no entity" from a real one if that ever
	// matters), and every subsequent id is strictly greater and unique.
	next atomic.Int32
}

// AllocID claims the next entity id: a unique, non-zero, monotonically increasing int32.
// Atomic pre-increment so a concurrent off-tick claim (the accept goroutine) and an on-tick
// claim (entity spawning) never collide and never cross game state — the same discipline as
// nextTeleportID. The id space is shared by players and entities so no id is ever reused.
func (a *EntityIDAllocator) AllocID() int32 {
	return a.next.Add(1)
}

// entityStore is the tick-owned entity collection: a by-id map plus a per-chunk-column grid
// bucket for the broad phase. Both maps are PLAIN maps mutated ONLY by the tick goroutine
// (TICK-05) — do NOT use xsync here (that is Phase 8). The tracker (Plan 06-02) reads the
// store via near() on the same goroutine; physics (Plan 06-03) moves entities via move().
//
// Bucketing is per chunk COLUMN (level.ChunkPos), NOT a quadtree (CLAUDE.md + 06-RESEARCH
// Pattern 1 / Alternatives): vanilla buckets entities per section/column, so a packed
// column key into a plain map is O(1), allocation-light, and cache-friendly. The
// server/internal/bvh.Tree is a profiling-driven fallback only and is deliberately NOT used
// here.
type entityStore struct {
	// byID is the authoritative entity collection keyed by the server-issued entity id.
	// add/get/remove/len operate here; the bucket is a derived index maintained alongside.
	byID map[int32]*Entity

	// buckets is the per-chunk-column grid index: column -> the entities whose position
	// falls in that column. near() walks the columns within range and returns their
	// entities; add/remove/move keep it consistent with the entity's current position. An
	// empty column slice is pruned on the last remove so the map does not leak keys.
	buckets map[level.ChunkPos][]*Entity
}

// newEntityStore constructs an empty tick-owned store. NewTickLoop calls it so the store is
// wired onto the loop from construction (a non-nil store the tracker can read).
func newEntityStore() *entityStore {
	return &entityStore{
		byID:    make(map[int32]*Entity),
		buckets: make(map[level.ChunkPos][]*Entity),
	}
}

// columnOf maps an entity's world position to its chunk column, reusing the SAME
// negative-correct floor-div helper applyInput uses (chunkCenterOf/floorDiv16) so an
// entity and a player at the same block land in the same bucket key. math.Floor first so a
// fractional position (e.g. x=8.5) maps to the integer block before the floor-div.
func columnOf(x, z float64) level.ChunkPos {
	return chunkCenterOf(int32(math.Floor(x)), int32(math.Floor(z)))
}

// add inserts an entity into the store and buckets it by its current column. Tick-owned
// (called only on the tick goroutine). Re-adding an id already present overwrites the by-id
// entry and re-buckets — but the intended mutation path for a position change is move().
func (s *entityStore) add(e *Entity) {
	s.byID[e.id] = e
	col := columnOf(e.x, e.z)
	s.buckets[col] = append(s.buckets[col], e)
}

// remove deletes an entity by id from both the by-id map and its column bucket. A missing
// id is a cheap no-op (never panics), so a double-remove is safe. Tick-owned.
func (s *entityStore) remove(id int32) {
	e, ok := s.byID[id]
	if !ok {
		return
	}
	delete(s.byID, id)
	s.unbucket(e, columnOf(e.x, e.z))
}

// unbucket removes e from the slice at col, pruning the column key when its slice empties so
// the bucket map does not accumulate empty columns. Swap-remove (order within a column is
// not significant). Internal helper; tick-owned.
func (s *entityStore) unbucket(e *Entity, col level.ChunkPos) {
	bucket := s.buckets[col]
	for i, x := range bucket {
		if x == e {
			last := len(bucket) - 1
			bucket[i] = bucket[last]
			bucket[last] = nil
			bucket = bucket[:last]
			if len(bucket) == 0 {
				delete(s.buckets, col)
			} else {
				s.buckets[col] = bucket
			}
			return
		}
	}
}

// move updates an entity's position and re-buckets it if the move crossed a chunk-column
// boundary, so near() always reflects the entity's current column (no stale bucket). This is
// the tick-owned mutation path Plan 06-03 (physics) uses to step an entity — it keeps the
// derived bucket index consistent with x/z. If the column is unchanged the bucket is left
// alone (only the cheap position fields update). Tick-owned.
func (s *entityStore) move(e *Entity, x, y, z float64) {
	oldCol := columnOf(e.x, e.z)
	newCol := columnOf(x, z)
	e.x, e.y, e.z = x, y, z
	if oldCol == newCol {
		return // same column: the bucket is still correct, nothing to re-index
	}
	s.unbucket(e, oldCol)
	s.buckets[newCol] = append(s.buckets[newCol], e)
}

// get returns the entity for id and whether it is present. An unknown/removed id returns
// (nil, false). Tick-owned read (the tracker calls it on the tick goroutine).
func (s *entityStore) get(id int32) (*Entity, bool) {
	e, ok := s.byID[id]
	return e, ok
}

// len returns the live entity count. Tick-owned.
func (s *entityStore) len() int { return len(s.byID) }

// all returns every entity in the store in ASCENDING id order. The deterministic order matters
// for the movement broadcast (tickEntityMovement) only for test reproducibility — each entity's
// send decision is independent — but a stable order keeps a multi-entity trace comparable run to
// run. Tick-owned; the returned slice is a fresh copy the caller may iterate freely.
func (s *entityStore) all() []*Entity {
	out := make([]*Entity, 0, len(s.byID))
	for _, e := range s.byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// near is the broad-phase query the tracker (Plan 06-02) consumes: it returns every entity
// whose chunk column is within rangeChunks columns (Chebyshev distance) of the column
// containing (x, z). It walks only the (2*rangeChunks+1)^2 candidate columns and collects
// their bucketed entities — it NEVER scans all entities — so the cost is bounded by the
// (clamped) track range, not the world entity count. A negative range is clamped to 0 (the
// single containing column). The returned slice is a fresh copy the caller may retain; the
// store's bucket slices are not aliased. Tick-owned.
func (s *entityStore) near(x, z float64, rangeChunks int) []*Entity {
	if rangeChunks < 0 {
		rangeChunks = 0
	}
	center := columnOf(x, z)
	var out []*Entity
	for dx := -rangeChunks; dx <= rangeChunks; dx++ {
		for dz := -rangeChunks; dz <= rangeChunks; dz++ {
			col := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
			if bucket, ok := s.buckets[col]; ok {
				out = append(out, bucket...)
			}
		}
	}
	return out
}
