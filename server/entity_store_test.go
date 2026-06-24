package server

import (
	"sync"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// entity_store_test.go covers the ENT-01 foundation: the monotonic entity-ID
// allocator (uniqueness + monotonicity + race-clean concurrent claims), the
// tick-owned entityStore (add/get/remove/len), the per-chunk-section grid bucketing
// broad-phase (near() returns exactly the in-range entities and re-buckets on move),
// and the Entity AABB helper sourced from the data/entity table (not hand-typed).
//
// The store and buckets are tick-owned game state (mutated only on the tick goroutine);
// the ALLOCATOR is the single off-tick crossing — an atomic counter claimed from the
// accept goroutine exactly like teleportSeq — so TestEntityIDAllocator's concurrent
// claim is the structural -race assertion (proven under the Docker -race gate, T-6-08).

// TestEntityIDAllocator asserts the allocator issues unique, monotonically increasing,
// non-zero ids; the first id is the documented start value (1); and concurrent claims
// (simulating the off-tick accept goroutine racing the tick) never collide. The race is
// proven structurally by the Docker -race gate; the uniqueness set here is the logical
// proof (T-6-07).
func TestEntityIDAllocator(t *testing.T) {
	var a EntityIDAllocator

	// The first issued id is deterministic and non-zero (pre-increment from 0 -> 1).
	if first := a.AllocID(); first != 1 {
		t.Fatalf("first AllocID() = %d, want 1 (deterministic non-zero start)", first)
	}

	// Sequential claims are strictly increasing and never repeat.
	const n = 1000
	prev := int32(1)
	seen := map[int32]bool{1: true}
	for i := 0; i < n; i++ {
		id := a.AllocID()
		if id <= prev {
			t.Fatalf("AllocID() = %d not strictly greater than previous %d (must be monotonic)", id, prev)
		}
		if seen[id] {
			t.Fatalf("AllocID() returned a duplicate id %d (must never reuse)", id)
		}
		seen[id] = true
		prev = id
	}

	// Concurrent claims from many goroutines (the off-tick accept-goroutine pattern):
	// every returned id must be distinct. Run under -race in the wave gate to prove the
	// atomic claim crosses no game state.
	var conc EntityIDAllocator
	const goroutines = 64
	const perG = 256
	results := make([][]int32, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			ids := make([]int32, perG)
			for i := 0; i < perG; i++ {
				ids[i] = conc.AllocID()
			}
			results[g] = ids
		}(g)
	}
	wg.Wait()

	all := make(map[int32]bool, goroutines*perG)
	for _, ids := range results {
		for _, id := range ids {
			if id == 0 {
				t.Fatalf("concurrent AllocID() returned 0 (ids must be non-zero)")
			}
			if all[id] {
				t.Fatalf("concurrent AllocID() returned a duplicate id %d (collision under contention)", id)
			}
			all[id] = true
		}
	}
	if len(all) != goroutines*perG {
		t.Fatalf("concurrent claims produced %d distinct ids, want %d", len(all), goroutines*perG)
	}
}

// TestEntityStore asserts the tick-owned add/get/remove/len semantics: an added entity
// is retrievable by id, a removed/unknown id returns (nil,false), and len() tracks the
// live count.
func TestEntityStore(t *testing.T) {
	s := newEntityStore()
	if s.len() != 0 {
		t.Fatalf("fresh store len() = %d, want 0", s.len())
	}

	e := NewEntity(42, entity.SulfurCube, 8.5, 64, 8.5)
	s.add(e)

	if s.len() != 1 {
		t.Fatalf("after add len() = %d, want 1", s.len())
	}
	got, ok := s.get(42)
	if !ok {
		t.Fatalf("get(42) ok = false, want true")
	}
	if got != e {
		t.Fatalf("get(42) returned a different pointer than was added")
	}

	// An unknown id returns (nil, false).
	if g, ok := s.get(999); ok || g != nil {
		t.Fatalf("get(999) = (%v,%v), want (nil,false)", g, ok)
	}

	s.remove(42)
	if s.len() != 0 {
		t.Fatalf("after remove len() = %d, want 0", s.len())
	}
	if g, ok := s.get(42); ok || g != nil {
		t.Fatalf("get(42) after remove = (%v,%v), want (nil,false)", g, ok)
	}

	// Removing an unknown id is a cheap no-op (never panics).
	s.remove(12345)
}

// TestEntityBucketing asserts the per-section grid broad-phase: near(x,z,range) returns
// exactly the entities whose chunk column is within range columns of (x,z), excludes
// those outside, and re-buckets an entity when it moves (re-add at a new position).
func TestEntityBucketing(t *testing.T) {
	s := newEntityStore()

	// Three entities: one at origin column (0,0), one one column east (block x=16 ->
	// column (1,0)), one far away (block x=1600 -> column (100,0)).
	near0 := NewEntity(1, entity.SulfurCube, 8, 64, 8)    // column (0,0)
	near1 := NewEntity(2, entity.SulfurCube, 16, 64, 8)   // column (1,0)
	far := NewEntity(3, entity.SulfurCube, 1600, 64, 8)   // column (100,0)
	s.add(near0)
	s.add(near1)
	s.add(far)

	// range 1 around (8,8) (column (0,0)) covers columns (-1..1, -1..1): includes near0
	// and near1, excludes far.
	got := s.near(8, 8, 1)
	if !containsEntity(got, near0) || !containsEntity(got, near1) {
		t.Fatalf("near(8,8,1) must include the origin-column and adjacent-column entities; got %d entities", len(got))
	}
	if containsEntity(got, far) {
		t.Fatalf("near(8,8,1) must exclude the far entity (column 100,0)")
	}

	// range 0 around (8,8) covers only column (0,0): includes near0, excludes near1.
	got0 := s.near(8, 8, 0)
	if !containsEntity(got0, near0) {
		t.Fatalf("near(8,8,0) must include the origin-column entity")
	}
	if containsEntity(got0, near1) {
		t.Fatalf("near(8,8,0) must exclude the adjacent-column entity at range 0")
	}

	// Move near1 from column (1,0) to the far column (100,0) and re-bucket it. After the
	// move it must drop out of the near query and appear near the far position.
	s.move(near1, 1600, 64, 8)
	gotAfter := s.near(8, 8, 1)
	if containsEntity(gotAfter, near1) {
		t.Fatalf("after moving near1 away, near(8,8,1) must no longer return it (stale bucket)")
	}
	gotFar := s.near(1600, 8, 1)
	if !containsEntity(gotFar, near1) {
		t.Fatalf("after moving near1 to the far column, near(1600,8,1) must return it (re-bucketed)")
	}
}

// TestEntityAABB asserts the Entity AABB helper is sourced from the data/entity table
// (Width/Height), not hand-typed: a SulfurCube at (x,y,z) produces a box centered on
// x/z with half-width Width/2, base at y, and top at y+Height.
func TestEntityAABB(t *testing.T) {
	const x, y, z = 8.5, 64.0, 8.5
	e := NewEntity(7, entity.SulfurCube, x, y, z)

	w := entity.SulfurCube.Width
	h := entity.SulfurCube.Height
	box := e.AABB()

	if box.Lower[0] != x-w/2 || box.Lower[2] != z-w/2 {
		t.Errorf("AABB lower horizontal = (%v,%v), want (%v,%v)", box.Lower[0], box.Lower[2], x-w/2, z-w/2)
	}
	if box.Upper[0] != x+w/2 || box.Upper[2] != z+w/2 {
		t.Errorf("AABB upper horizontal = (%v,%v), want (%v,%v)", box.Upper[0], box.Upper[2], x+w/2, z+w/2)
	}
	if box.Lower[1] != y {
		t.Errorf("AABB base y = %v, want %v (base at feet)", box.Lower[1], y)
	}
	if box.Upper[1] != y+h {
		t.Errorf("AABB top y = %v, want %v (y+Height)", box.Upper[1], y+h)
	}
	// The dims must come from the table, not be hand-typed — a non-zero footprint proves
	// Width/Height were copied.
	if e.width != w || e.height != h {
		t.Errorf("entity dims (%v,%v) != table dims (%v,%v) — must be copied from data/entity", e.width, e.height, w, h)
	}
}

// containsEntity reports whether e is present in the slice (broad-phase result helper).
func containsEntity(es []*Entity, e *Entity) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}
