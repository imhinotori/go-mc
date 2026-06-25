package placement

import (
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// recordingPlacer is a stub ConfiguredFeaturePlacer that records every anchor it was
// called at (proving the fold produces the right anchors + calls the body at each)
// and reports `placed` to drive place()'s return value.
type recordingPlacer struct {
	calls  []BlockPos
	placed bool
}

func (rp *recordingPlacer) place(_ PlacementContext, _ levelgen.RandomSource, pos BlockPos) bool {
	rp.calls = append(rp.calls, pos)
	return rp.placed
}

// TestPlaceFold pins the ordered count -> in_square -> heightmap fold: exactly N
// anchors, each jittered in-square then projected to the heightmap top, AND the rng
// left in the exact post-fold state a hand-traced sequential vanilla fold leaves it.
func TestPlaceFold(t *testing.T) {
	const seed = int64(0xBEEF)
	const minY, height = -64, 384

	// Fake context: a MOTION_BLOCKING heightmap that returns 64 + (x%8) for any (x,z)
	// so projection is deterministic and distinct per column.
	ctx := newFakeContext(minY, height)
	heightAt := func(x int) int { return 64 + (((x % 8) + 8) % 8) }

	origin := BlockPos{X: 100, Y: 0, Z: 200}

	// --- Hand-traced reference fold on a plain LCG (the determinism oracle) ---
	// count(constant 3): 0 draws, 3 copies of origin.
	// in_square per copy: nextInt(16)+x, then nextInt(16)+z (2 draws each, x first).
	// heightmap: 0 draws, project to heightAt(x).
	ref := levelgen.NewLegacyRandomSource(seed)
	var wantAnchors []BlockPos
	for i := 0; i < 3; i++ {
		x := int(ref.NextIntN(16)) + origin.X
		z := int(ref.NextIntN(16)) + origin.Z
		y := heightAt(x)
		// Register the projected column so the modifier reads the same height.
		ctx.setHeight(MotionBlocking, x, z, y)
		wantAnchors = append(wantAnchors, BlockPos{X: x, Y: y, Z: z})
	}
	// The hand trace consumed 3*2 = 6 draws; capture the post-fold state by draw count.

	// --- The real bound fold ---
	placer := &recordingPlacer{placed: true}
	bpf := &BoundPlacedFeature{
		Placement: []PlacementModifier{
			newCount(&intProvider{kind: intConstant, value: 3}),
			InSquare{},
			Heightmap{heightmap: MotionBlocking},
		},
		Configured: placer,
	}

	rng := newCounter(seed)
	got := bpf.Place(ctx, rng, origin)

	if !got {
		t.Fatalf("place() = false, want true (placer reported placed)")
	}
	if len(placer.calls) != 3 {
		t.Fatalf("placer called %d times, want 3", len(placer.calls))
	}
	for i, p := range placer.calls {
		if p != wantAnchors[i] {
			t.Fatalf("anchor %d = %v, want %v", i, p, wantAnchors[i])
		}
	}

	// Exact post-fold rng draw count: count(0) + 3 * in_square(2) + heightmap(0) = 6.
	if rng.draws != 6 {
		t.Fatalf("post-fold rng drew %d, want 6 (3 in_square pairs)", rng.draws)
	}

	// Exact post-fold rng STATE: the bound fold's rng must equal the oracle's after
	// the same 6 draws — the next draw from each must match.
	if next, wantNext := rng.NextLong(), ref.NextLong(); next != wantNext {
		t.Fatalf("post-fold rng state diverged: next draw %d, want %d", next, wantNext)
	}
}

// TestPlaceEmptyChain: a rarity_filter that rejects yields 0 anchors -> place() false
// and the placer is never called.
func TestPlaceEmptyChain(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	origin := BlockPos{X: 0, Y: 0, Z: 0}

	// Find a seed whose first NextFloat is NOT < 1/1000000 (almost any seed). Use a
	// huge chance so rarity_filter overwhelmingly rejects.
	placer := &recordingPlacer{placed: true}
	bpf := &BoundPlacedFeature{
		Placement: []PlacementModifier{
			newRarityFilter(1000000),
			InSquare{},
		},
		Configured: placer,
	}

	rng := newCounter(1)
	got := bpf.Place(ctx, rng, origin)
	if got {
		t.Fatalf("place() = true, want false (rarity rejected)")
	}
	if len(placer.calls) != 0 {
		t.Fatalf("placer called %d times on a rejected chain, want 0", len(placer.calls))
	}
	// The rarity_filter still consumed exactly its one NextFloat; in_square then ran
	// on zero positions (0 draws).
	if rng.draws != 1 {
		t.Fatalf("empty chain drew %d, want 1 (the rarity NextFloat only)", rng.draws)
	}
}

// TestPlaceThreadsOneRng: two features over the same seed must consume the rng
// identically — proving a single threaded rng, not per-modifier forks.
func TestPlaceThreadsOneRng(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	origin := BlockPos{X: 10, Y: 0, Z: 10}

	build := func() *BoundPlacedFeature {
		return &BoundPlacedFeature{
			Placement: []PlacementModifier{
				newCount(&intProvider{kind: intUniform, minVal: 1, maxVal: 3}),
				InSquare{},
			},
			Configured: &recordingPlacer{placed: false},
		}
	}

	rngA := newCounter(555)
	build().Place(ctx, rngA, origin)
	rngB := newCounter(555)
	build().Place(ctx, rngB, origin)

	if rngA.draws != rngB.draws {
		t.Fatalf("identical features drew differently: %d vs %d", rngA.draws, rngB.draws)
	}
	if a, b := rngA.NextLong(), rngB.NextLong(); a != b {
		t.Fatalf("post-fold rng states diverged for identical features: %d vs %d", a, b)
	}
}
