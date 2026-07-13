package placement

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
)

// idSetBenchInputs are representative real predicate id-sets (matching_blocks / replaceable).
var idSetBenchInputs = [][]string{
	{"minecraft:air", "minecraft:cave_air", "minecraft:void_air"},
	replaceableBlockIDs,
	{"minecraft:stone"},
	{"minecraft:grass_block", "minecraft:dirt", "minecraft:podzol"},
	{"minecraft:water", "minecraft:lava"},
}

// BenchmarkIdSetToStateSetMemo measures the memoized (warm-cache) path — the steady
// state a walking player hits, where every placed_feature Bind re-requests the same
// handful of id-sets. It is the after-number for the per-chunk re-parse the memoization
// removed (the cold build scans all ~30k block.StateList once per distinct set).
func BenchmarkIdSetToStateSetMemo(b *testing.B) {
	// Warm the cache once (mirrors the first chunk); then the per-op cost is a map read.
	for _, in := range idSetBenchInputs {
		_ = idSetToStateSet(in)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idSetToStateSet(idSetBenchInputs[i%len(idSetBenchInputs)])
	}
}

// BenchmarkIdSetToStateSetCold measures the un-memoized full-StateList scan (the old
// per-call cost) via the extracted builder — the before-number.
func BenchmarkIdSetToStateSetCold(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildIDSetToStateSet(idSetBenchInputs[i%len(idSetBenchInputs)])
	}
}

// TestIdSetMemoIdentical proves the memoized result is value-identical to a fresh scan
// (byte-identical membership) for every representative input — the correctness guard for
// the optimization.
func TestIdSetMemoIdentical(t *testing.T) {
	for _, in := range idSetBenchInputs {
		got := idSetToStateSet(in)
		want := buildIDSetToStateSet(in)
		if len(got) != len(want) {
			t.Fatalf("len mismatch for %v: got %d want %d", in, len(got), len(want))
		}
		for sid := range want {
			if !got[sid] {
				t.Fatalf("missing state %d for %v", sid, in)
			}
		}
		for sid := range got {
			if !want[sid] {
				t.Fatalf("extra state %d for %v", sid, in)
			}
		}
	}
	// Order/dup-independence: shuffled+duplicated input yields the same cached set.
	a := idSetToStateSet([]string{"minecraft:water", "minecraft:lava"})
	c := idSetToStateSet([]string{"minecraft:lava", "minecraft:water", "minecraft:lava"})
	if len(a) != len(c) {
		t.Fatalf("order/dup key mismatch: %d vs %d", len(a), len(c))
	}
	_ = block.StateList
}
