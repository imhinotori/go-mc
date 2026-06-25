package world

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/level"
)

// seamTestGen builds the deterministic generator the seam acceptance suite runs against.
// Superflat is used: it is fast (important for the -race -count=10 gate) and its no-op
// Decorate degenerates the reorder test to the terrain-determinism guarantee — which is
// EXACTLY the GEN2-02 property under test (the staging/scheduler SEAM must introduce no
// order-dependence). The seam (staging map, single scheduler goroutine, Neighborhood 3x3
// proxy, exactly-once emit) is generator-agnostic.
func seamTestGen() Generator { return NewSuperflat(24, -64, -1) }

// generateRegion builds a Worker, runs it under a cancelable ctx, Requests every position
// in `order`, and drains Results() until every REQUESTED position has been emitted (the
// scheduler auto-requests + carves their neighbors so each requested center decorates). It
// returns each emitted center serialized via chunk.WriteTo, keyed by pos. Auto-requested
// neighbor centers may also appear on Results() — they are collected too (keyed by pos),
// so a caller comparing two orders compares every position present in both.
func generateRegion(t *testing.T, gen Generator, order []level.ChunkPos) map[level.ChunkPos][]byte {
	t.Helper()

	// A buffer comfortably larger than the working set (the requested region + its
	// auto-requested neighbor ring) so no neighbor request is ever dropped — convergence.
	w := NewWorker(gen, "", 512)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	want := make(map[level.ChunkPos]bool, len(order))
	for _, p := range order {
		want[p] = true
	}

	for _, p := range order {
		w.Request(p)
	}

	out := make(map[level.ChunkPos][]byte)
	remaining := len(want)
	deadline := time.After(30 * time.Second)
	for remaining > 0 {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("ChunkResult.Err = %v", res.Err)
			}
			if res.Chunk == nil {
				t.Fatalf("ChunkResult.Chunk is nil for %v", res.Pos)
			}
			if _, seen := out[res.Pos]; !seen {
				var buf bytes.Buffer
				if _, err := res.Chunk.WriteTo(&buf); err != nil {
					t.Fatalf("WriteTo(%v): %v", res.Pos, err)
				}
				out[res.Pos] = buf.Bytes()
				if want[res.Pos] {
					remaining--
				}
			}
		case <-deadline:
			t.Fatalf("timed out: %d of %d requested centers not emitted", remaining, len(want))
		}
	}
	return out
}

// region5x5 is the (-2..2, -2..2) center set — 25 chunks; the scheduler auto-requests the
// surrounding ring so every one of the 25 has a fully carved 3x3 and decorates.
func region5x5() []level.ChunkPos {
	var ps []level.ChunkPos
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-2); z <= 2; z++ {
			ps = append(ps, level.ChunkPos{x, z})
		}
	}
	return ps
}

// shuffleOrder returns a FIXED (deterministic) reordering of ps — a reversal interleave —
// so the two runs differ in request order without introducing test-run nondeterminism.
func shuffleOrder(ps []level.ChunkPos) []level.ChunkPos {
	out := make([]level.ChunkPos, 0, len(ps))
	for i := len(ps) - 1; i >= 0; i-- {
		out = append(out, ps[i])
	}
	// Rotate by a prime offset so it is neither the original nor a pure reversal pairing.
	const off = 7
	rot := make([]level.ChunkPos, 0, len(out))
	for i := range out {
		rot = append(rot, out[(i+off)%len(out)])
	}
	return rot
}

// TestDecorationReorderIdentical is THE GEN2-02 acceptance gate: a 5x5 region generated in
// two DIFFERENT request orders must serialize to byte-identical chunks for every position.
// This is the order-independence proof for the staging/scheduler seam (Phase 10 Success
// Criterion 3). For the no-op Decorate it degenerates to the terrain-determinism guarantee
// — the point being the SEAM (staging order, scheduler scan order, auto-request order)
// introduces NO order-dependence in the emitted bytes.
func TestDecorationReorderIdentical(t *testing.T) {
	region := region5x5()

	a := generateRegion(t, seamTestGen(), region)
	b := generateRegion(t, seamTestGen(), shuffleOrder(region))

	checked := 0
	for p, ab := range a {
		bb, ok := b[p]
		if !ok {
			continue // only compare positions present in both runs
		}
		if !bytes.Equal(ab, bb) {
			t.Fatalf("chunk %v differs between request orders: %d vs %d bytes", p, len(ab), len(bb))
		}
		checked++
	}
	if checked < len(region) {
		t.Fatalf("only %d of %d region centers were comparable (want all %d)", checked, len(region), len(region))
	}
}

// TestEmitOnce asserts each requested center is emitted EXACTLY ONCE — the decorated-flag +
// all-9-carved guard must never double-emit (threat T-10-08). It Requests a small region
// and counts every pos appearing on Results() over a drain window.
func TestEmitOnce(t *testing.T) {
	region := region5x5()
	w := NewWorker(seamTestGen(), "", 512)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	want := make(map[level.ChunkPos]bool, len(region))
	for _, p := range region {
		want[p] = true
		w.Request(p)
	}

	counts := make(map[level.ChunkPos]int)
	got := 0
	deadline := time.After(30 * time.Second)
	// Drain until every requested center has appeared at least once, then keep draining a
	// short tail to catch any erroneous duplicate.
drain:
	for {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("ChunkResult.Err = %v", res.Err)
			}
			counts[res.Pos]++
			if want[res.Pos] && counts[res.Pos] == 1 {
				got++
			}
		case <-time.After(500 * time.Millisecond):
			if got >= len(want) {
				break drain
			}
		case <-deadline:
			t.Fatalf("timed out: only %d of %d requested centers emitted", got, len(want))
		}
	}

	for p, n := range counts {
		if n != 1 {
			t.Fatalf("chunk %v emitted %d times, want exactly 1 (double-emit)", p, n)
		}
	}
}

// TestEmitOnceUnderHold pins the D2 Option-Y rule under MAXIMALLY adjacent wanted centers:
// a 3x3 block of wanted centers (every center has up to 8 wanted neighbors). The hold gate
// (emit a center only once every wanted neighbor is decorated) must STILL emit each wanted
// center exactly once — no double-emit (the hold re-checks an already-emitted center), no
// hold-deadlock (a center's wanted neighbors are all requested, so they all decorate). It
// also confirms every emitted center is StatusFull (decorated before emit).
func TestEmitOnceUnderHold(t *testing.T) {
	// A 3x3 block of wanted centers (-1..1, -1..1) — the densest adjacency.
	var region []level.ChunkPos
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			region = append(region, level.ChunkPos{x, z})
		}
	}

	w := NewWorker(seamTestGen(), "", 512)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	want := make(map[level.ChunkPos]bool, len(region))
	for _, p := range region {
		want[p] = true
		w.Request(p)
	}

	counts := make(map[level.ChunkPos]int)
	got := 0
	deadline := time.After(30 * time.Second)
drain:
	for {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("ChunkResult.Err = %v", res.Err)
			}
			if want[res.Pos] {
				if res.Chunk == nil || res.Chunk.Status != level.StatusFull {
					t.Fatalf("wanted center %v emitted not-StatusFull (decorated-before-emit violated)", res.Pos)
				}
			}
			counts[res.Pos]++
			if want[res.Pos] && counts[res.Pos] == 1 {
				got++
			}
		case <-time.After(500 * time.Millisecond):
			if got >= len(want) {
				break drain
			}
		case <-deadline:
			t.Fatalf("hold-deadlock: only %d of %d adjacent wanted centers emitted", got, len(want))
		}
	}

	for p, n := range counts {
		if want[p] && n != 1 {
			t.Fatalf("wanted center %v emitted %d times under the hold rule, want exactly 1", p, n)
		}
	}
}

// TestNeighborhoodCompletion asserts a SINGLE Request(C) with nothing else converges to C
// emitted: the scheduler auto-requests C's 8 neighbors, carves them, and decorates C only
// once all 8 are carved (the hold-until-neighbors-carved + auto-request path). Proves the
// neighborhood-completion gate without external re-requesting.
func TestNeighborhoodCompletion(t *testing.T) {
	w := NewWorker(seamTestGen(), "", 512)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	center := level.ChunkPos{0, 0}
	w.Request(center)

	deadline := time.After(15 * time.Second)
	for {
		select {
		case res := <-w.Results():
			if res.Err != nil {
				t.Fatalf("ChunkResult.Err = %v", res.Err)
			}
			if res.Pos == center {
				if res.Chunk == nil {
					t.Fatalf("center %v emitted with nil chunk", center)
				}
				if res.Chunk.Status != level.StatusFull {
					t.Fatalf("center %v status = %q, want %q (decorated)", center, res.Chunk.Status, level.StatusFull)
				}
				return // converged
			}
		case <-deadline:
			t.Fatalf("single Request(%v) never converged to an emit", center)
		}
	}
}
