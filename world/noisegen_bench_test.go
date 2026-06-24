package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// BenchmarkGenerateOneChunk measures the full Generate(pos) pipeline cost per chunk — the
// number that decides whether a real vanilla 26.2 client can be fed chunks fast enough to
// avoid the "Loading terrain" timeout/disconnect. The biome lookup (multi-noise findValue)
// was a linear O(n) scan over the ~7594 overworld parameter boxes per quart cell, which
// dominated this number (~985ms/chunk in the CPU profile). The Climate RTree port turns it
// into an O(log n)-ish pruned search; this benchmark is the before/after gate.
//
// Use distinct positions per iteration so per-chunk work is not amortized into a single
// generator's caches in a way that hides the real per-chunk cost.
func BenchmarkGenerateOneChunk(b *testing.B) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pos := level.ChunkPos{int32(i & 31), int32((i >> 5) & 31)}
		ch := g.Generate(pos)
		if ch == nil {
			b.Fatal("nil chunk")
		}
	}
}
