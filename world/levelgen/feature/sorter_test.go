package feature

import (
	"testing"
)

// mkPF builds a synthetic placed_feature pointer for the sorter tests. Only its
// identity (the pointer) and ID matter to the sorter — it never parses config — so the
// tests are independent of the embed.
func mkPF(id string) *PlacedFeature { return &PlacedFeature{ID: id} }

// biomeSteps wraps a single biome's per-step lists into the 11-element shape
// BuildFeaturesPerStep expects, placing `lists` starting at step 0.
func biomeSteps(lists ...[]*PlacedFeature) [][]*PlacedFeature {
	steps := make([][]*PlacedFeature, DecorationStepCount)
	for i := range steps {
		steps[i] = nil
	}
	for i, l := range lists {
		if i < DecorationStepCount {
			steps[i] = l
		}
	}
	return steps
}

// TestSorterDedup: biome A=[p1,p2], biome B=[p2,p3] at step 0 dedups p2 to one global
// index and yields the stable order p1,p2,p3.
func TestSorterDedup(t *testing.T) {
	p1, p2, p3 := mkPF("p1"), mkPF("p2"), mkPF("p3")

	biomeA := biomeSteps([]*PlacedFeature{p1, p2})
	biomeB := biomeSteps([]*PlacedFeature{p2, p3})

	fs, err := BuildFeaturesPerStep([][][]*PlacedFeature{biomeA, biomeB})
	if err != nil {
		t.Fatalf("BuildFeaturesPerStep: %v", err)
	}

	got := fs.PerStep(0)
	want := []*PlacedFeature{p1, p2, p3}
	if len(got) != len(want) {
		t.Fatalf("step 0 len = %d (%v), want %d", len(got), idsOf(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step 0 order = %v, want [p1 p2 p3]", idsOf(got))
		}
	}

	// p2 must have ONE global index, identical no matter which biome referenced it.
	i1, ok1 := fs.Index(p1)
	i2, ok2 := fs.Index(p2)
	i3, ok3 := fs.Index(p3)
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("Index missing: p1=%v p2=%v p3=%v", ok1, ok2, ok3)
	}
	if i1 != 0 || i2 != 1 || i3 != 2 {
		t.Fatalf("indices = p1:%d p2:%d p3:%d, want 0,1,2", i1, i2, i3)
	}
}

// TestSorterCycleErrors: biome A=[p1,p2], biome B=[p2,p1] is a contradiction (A says
// p1<p2, B says p2<p1) -> a cycle -> error.
func TestSorterCycleErrors(t *testing.T) {
	p1, p2 := mkPF("p1"), mkPF("p2")
	biomeA := biomeSteps([]*PlacedFeature{p1, p2})
	biomeB := biomeSteps([]*PlacedFeature{p2, p1})

	_, err := BuildFeaturesPerStep([][][]*PlacedFeature{biomeA, biomeB})
	if err == nil {
		t.Fatalf("BuildFeaturesPerStep accepted a contradictory cross-biome order; want a cycle error")
	}
}

// TestSorterDeterministic: shuffling the biome input order yields IDENTICAL per-step
// lists + indices (the toposort breaks ties by first-seen, derived from input order, so
// the OUTPUT is independent of how the caller iterated the biome set — given the same
// total constraint set).
func TestSorterDeterministic(t *testing.T) {
	p1, p2, p3, p4 := mkPF("p1"), mkPF("p2"), mkPF("p3"), mkPF("p4")

	// Three biomes with overlapping, non-contradictory chains:
	//   A: p1 -> p2 -> p3
	//   B: p2 -> p3 -> p4
	//   C: p1 -> p4
	// A valid topo order: p1, p2, p3, p4.
	bA := biomeSteps([]*PlacedFeature{p1, p2, p3})
	bB := biomeSteps([]*PlacedFeature{p2, p3, p4})
	bC := biomeSteps([]*PlacedFeature{p1, p4})

	fs1, err := BuildFeaturesPerStep([][][]*PlacedFeature{bA, bB, bC})
	if err != nil {
		t.Fatalf("order 1: %v", err)
	}
	// Shuffle the biome order (C, A, B) — same constraint set, different iteration.
	fs2, err := BuildFeaturesPerStep([][][]*PlacedFeature{bC, bA, bB})
	if err != nil {
		t.Fatalf("order 2: %v", err)
	}

	l1, l2 := fs1.PerStep(0), fs2.PerStep(0)
	if len(l1) != len(l2) {
		t.Fatalf("len differs across biome orders: %d vs %d", len(l1), len(l2))
	}
	for i := range l1 {
		if l1[i] != l2[i] {
			t.Fatalf("order differs across biome orders: %v vs %v", idsOf(l1), idsOf(l2))
		}
	}
	// And the order must respect every chain constraint (p1<p2<p3<p4, p1<p4).
	pos := make(map[*PlacedFeature]int)
	for i, pf := range l1 {
		pos[pf] = i
	}
	assertBefore(t, pos, p1, p2)
	assertBefore(t, pos, p2, p3)
	assertBefore(t, pos, p3, p4)
	assertBefore(t, pos, p1, p4)
}

func assertBefore(t *testing.T, pos map[*PlacedFeature]int, a, b *PlacedFeature) {
	t.Helper()
	if pos[a] >= pos[b] {
		t.Fatalf("constraint violated: %q (idx %d) must precede %q (idx %d)", a.ID, pos[a], b.ID, pos[b])
	}
}

func idsOf(pfs []*PlacedFeature) []string {
	out := make([]string, len(pfs))
	for i, pf := range pfs {
		out[i] = pf.ID
	}
	return out
}
