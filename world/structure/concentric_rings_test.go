package structure

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// referenceRingPositionsNoBiome independently re-implements the generateRingPositions spiral
// (radius/angle/round math, the dedicated raw-world-seed LCG) WITHOUT the biome adjustment —
// the raw rounded chunk positions. This is the oracle TestGenerateRingPositions checks the
// production code against (with an accept-all biome stub, the production adjust returns the
// center chunk == the raw position, so the two must agree bit-for-bit).
func referenceRingPositionsNoBiome(worldSeed int64, p ConcentricRingsStructurePlacement) []level.ChunkPos {
	if p.Count == 0 {
		return nil
	}
	distance, count, spread := p.Distance, p.Count, p.Spread
	out := make([]level.ChunkPos, 0, count)

	rng := levelgen.NewLegacyRandomSource(0)
	rng.SetSeed(worldSeed)
	angle := rng.NextDouble() * math.Pi * 2.0
	groupIdx, ring := 0, 0

	for i := 0; i < count; i++ {
		dist := float64(4*distance+distance*ring*6) + (rng.NextDouble()-0.5)*float64(distance)*2.5
		x := int(math.Round(math.Cos(angle) * dist))
		z := int(math.Round(math.Sin(angle) * dist))
		// The production code forks the RNG per position for the biome reservoir; mirror the
		// fork so the parent RNG stream stays in lockstep even though the accept-all oracle
		// ignores the fork's draws.
		rng.Fork()
		out = append(out, level.ChunkPos{int32(x), int32(z)})

		angle += (math.Pi * 2.0) / float64(spread)
		groupIdx++
		if groupIdx == spread {
			ring++
			groupIdx = 0
			spread = spread + 2*spread/(ring+1)
			if rem := count - i; spread > rem {
				spread = rem
			}
			angle = rng.NextDouble() * math.Pi * 2.0
		}
	}
	return out
}

// referenceRingPositions is the accept-all oracle: the raw spiral positions (the accept-all
// biome stub snaps each candidate back to its own chunk).
func referenceRingPositions(worldSeed int64, p ConcentricRingsStructurePlacement) []level.ChunkPos {
	return referenceRingPositionsNoBiome(worldSeed, p)
}

// biomeTypeOf resolves a namespaced biome id to its levelbiome.Type by scanning the
// generated name table (the test needs concrete Types to feed the biomeAt stub).
func biomeTypeOf(t *testing.T, id string) levelbiome.Type {
	t.Helper()
	for i := 0; i < 4096; i++ {
		bt := levelbiome.Type(i)
		if bt.String() == id {
			return bt
		}
	}
	t.Fatalf("biome id %q not found in the name table", id)
	return 0
}

// centerAcceptStub returns a biomeAt that accepts ONLY the exact spiral-center block columns
// of the raw rounded positions (precomputed from the oracle). The radius-0 sample of each
// candidate hits its own center (an accepted cell -> a single reservoir match); the off-center
// search cells land between widely-spaced positions and are rejected. adjustToPreferredBiome
// therefore snaps each position back to its own chunk, so the biome-adjusted result equals the
// raw rounded spiral the in-test oracle computes.
func centerAcceptStub(t *testing.T, seed int64, p ConcentricRingsStructurePlacement) BiomeAt {
	t.Helper()
	plains := biomeTypeOf(t, "minecraft:plains")
	ocean := biomeTypeOf(t, "minecraft:ocean")
	raw := referenceRingPositionsNoBiome(seed, p)
	centers := make(map[[2]int]bool, len(raw))
	for _, cp := range raw {
		// The radius-0 sample column: chunk-center block snapped to quart (= chunk*16+8).
		centers[[2]int{int(cp[0])*16 + 8, int(cp[1])*16 + 8}] = true
	}
	return func(wx, wy, wz int) levelbiome.Type {
		if centers[[2]int{wx, wz}] {
			return plains
		}
		return ocean
	}
}

// TestConcentricRingsParse pins the strongholds structure_set as concentric_rings with
// the exact embedded params (count 128 / distance 32 / spread 3 / salt 0 / preferred
// #stronghold_biased_to). A divergence moves every stronghold.
func TestConcentricRingsParse(t *testing.T) {
	p, err := LoadConcentricRingsPlacement("minecraft:strongholds")
	if err != nil {
		t.Fatalf("LoadConcentricRingsPlacement: %v", err)
	}
	if p.Count != 128 {
		t.Errorf("count = %d, want 128", p.Count)
	}
	if p.Distance != 32 {
		t.Errorf("distance = %d, want 32", p.Distance)
	}
	if p.Spread != 3 {
		t.Errorf("spread = %d, want 3", p.Spread)
	}
	if p.Salt != 0 {
		t.Errorf("salt = %d, want 0", p.Salt)
	}
	if p.PreferredBiomes != "#minecraft:stronghold_biased_to" {
		t.Errorf("preferred_biomes = %q, want #minecraft:stronghold_biased_to", p.PreferredBiomes)
	}
}

// TestStrongholdBiasedTo pins the preferred-biome set: the 38-entry list loads, plains/
// desert/taiga are IN, ocean / a nether biome are NOT.
func TestStrongholdBiasedTo(t *testing.T) {
	set, err := LoadStrongholdBiasedTo()
	if err != nil {
		t.Fatalf("LoadStrongholdBiasedTo: %v", err)
	}
	if len(set) != 38 {
		t.Errorf("preferred set size = %d, want 38", len(set))
	}
	for _, in := range []string{"minecraft:plains", "minecraft:desert", "minecraft:taiga", "minecraft:sulfur_caves"} {
		if !set[in] {
			t.Errorf("expected %q IN the preferred set", in)
		}
	}
	for _, out := range []string{"minecraft:ocean", "minecraft:nether_wastes", "minecraft:the_end"} {
		if set[out] {
			t.Errorf("expected %q NOT in the preferred set", out)
		}
	}
}

// TestGenerateRingPositions pins the spiral against an in-test reference computed BY the
// same algorithm (radius/angle/round math, the dedicated worldSeed ring seed via the
// legacy LCG), with an accept-all biome stub so each position keeps its rounded value.
func TestGenerateRingPositions(t *testing.T) {
	const seed int64 = 1234567

	p := ConcentricRingsStructurePlacement{Count: 128, Distance: 32, Spread: 3, Salt: 0}
	preferred := map[string]bool{"minecraft:plains": true}

	got := generateRingPositions(seed, p, centerAcceptStub(t, seed, p), preferred)
	if len(got) != 128 {
		t.Fatalf("ring count = %d, want 128", len(got))
	}

	// Reference: the bit-exact spiral; the center-only stub snaps each position back to its
	// own chunk, so each adjusted position == its rounded spiral pos.
	want := referenceRingPositions(seed, p)
	if len(want) != len(got) {
		t.Fatalf("reference count = %d, got = %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ring[%d] = %v, want %v (a draw-order/rounding divergence)", i, got[i], want[i])
		}
	}
}

// TestRingSeed pins the dedicated derivation: the ring RNG is the legacy LCG seeded with
// the RAW world seed (concentricRingsSeed == levelSeed in createForNormal — NOT the
// structure-salt path). The first angle = nextDouble()*PI*2 from that exact seeding.
func TestRingSeed(t *testing.T) {
	const seed int64 = 99
	p := ConcentricRingsStructurePlacement{Count: 1, Distance: 32, Spread: 3, Salt: 0}
	preferred := map[string]bool{"minecraft:plains": true}

	got := generateRingPositions(seed, p, centerAcceptStub(t, seed, p), preferred)
	want := referenceRingPositions(seed, p)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("count got=%d want=%d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Fatalf("first ring pos = %v, want %v (wrong ring-seed derivation)", got[0], want[0])
	}
}

// TestGenerateRingPositionsBiomeAdjust proves the biome-search loop fires: a stub that
// rejects the center sample but accepts a shifted one moves the position off the raw
// rounded spiral pos (the #stronghold_biased_to adjustment).
func TestGenerateRingPositionsBiomeAdjust(t *testing.T) {
	const seed int64 = 7
	p := ConcentricRingsStructurePlacement{Count: 1, Distance: 32, Spread: 3, Salt: 0}
	preferred := map[string]bool{"minecraft:plains": true}

	// Reject everything within 16 blocks of the spiral center; accept further out.
	raw := referenceRingPositionsNoBiome(seed, p)
	center := raw[0]
	cx, cz := int(center[0])*16, int(center[1])*16
	stub := func(wx, wy, wz int) levelbiome.Type {
		dx, dz := wx-cx, wz-cz
		if dx*dx+dz*dz < 16*16 {
			return biomeTypeOf(t, "minecraft:ocean") // rejected
		}
		return biomeTypeOf(t, "minecraft:plains") // accepted
	}
	got := generateRingPositions(seed, p, stub, preferred)
	if len(got) != 1 {
		t.Fatalf("count = %d, want 1", len(got))
	}
	if got[0] == center {
		t.Fatalf("position not adjusted: still %v despite center-biome rejection", got[0])
	}
}

// --- StrongholdRingState (Task 2) ---

// TestStrongholdRingState pins the sync.Once single-compute under concurrent access and
// the immutable lock-free read.
func TestStrongholdRingState(t *testing.T) {
	const seed int64 = 4242
	var computes int64
	preferred := map[string]bool{"minecraft:plains": true}
	biomeAt := func(wx, wy, wz int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }

	rs := newStrongholdRingStateInstrumented(seed, biomeAt, preferred, &computes)

	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rs.isPlacementChunk(level.ChunkPos{int32(i), int32(i)})
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&computes); got != 1 {
		t.Fatalf("compute-count = %d, want 1 (sync.Once)", got)
	}
}

// TestIsPlacementChunk pins the contains-test: every ring position is a placement chunk;
// a far-off non-ring chunk is not. Pure over (worldSeed).
func TestIsPlacementChunk(t *testing.T) {
	const seed int64 = 555
	preferred, err := LoadStrongholdBiasedTo()
	if err != nil {
		t.Fatalf("LoadStrongholdBiasedTo: %v", err)
	}
	biomeAt := func(wx, wy, wz int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }

	rs := NewStrongholdRingState(seed, biomeAt, preferred)
	positions := rs.RingPositions()
	if len(positions) != 128 {
		t.Fatalf("ring positions = %d, want 128", len(positions))
	}
	for _, pos := range positions {
		if !rs.isPlacementChunk(pos) {
			t.Errorf("ring pos %v not reported as placement chunk", pos)
		}
	}
	// A chunk far outside any ring (the rings sit at radius >= ~floor distance) — pick an
	// impossible coordinate not in the list.
	far := level.ChunkPos{1 << 20, 1 << 20}
	if rs.isPlacementChunk(far) {
		t.Errorf("non-ring chunk %v wrongly reported as placement chunk", far)
	}

	// Purity over worldSeed: a second state with the SAME seed yields the SAME answers.
	rs2 := NewStrongholdRingState(seed, biomeAt, preferred)
	for _, pos := range positions {
		if !rs2.isPlacementChunk(pos) {
			t.Errorf("re-seeded state diverged at %v (impure over seed)", pos)
		}
	}
}

// TestStrongholdStart pins the placement-half generator: a ring chunk yields one start
// (Structure minecraft:stronghold, ChunkPos==pos) — now with the REAL recursive piece tree
// filled (15-03 replaced 15-02's byte-inert stub); a non-ring chunk yields nil.
func TestStrongholdStart(t *testing.T) {
	const seed int64 = 808
	preferred, err := LoadStrongholdBiasedTo()
	if err != nil {
		t.Fatalf("LoadStrongholdBiasedTo: %v", err)
	}
	biomeAt := func(wx, wy, wz int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }
	rs := NewStrongholdRingState(seed, biomeAt, preferred)
	gen := NewStrongholdStartGen(rs)

	sampler := stubSurfaceSampler{y: 64}
	ringPos := rs.RingPositions()[0]

	starts := gen.GenerateStarts(seed, ringPos, sampler, biomeAt)
	if len(starts) != 1 {
		t.Fatalf("ring chunk start count = %d, want 1", len(starts))
	}
	st := starts[0]
	if st.Structure != "minecraft:stronghold" {
		t.Errorf("structure = %q, want minecraft:stronghold", st.Structure)
	}
	if st.ChunkPos != ringPos {
		t.Errorf("anchor ChunkPos = %v, want %v", st.ChunkPos, ringPos)
	}
	if len(st.Pieces) == 0 {
		t.Errorf("pieces = 0, want >=1 (15-03 fills the recursive stronghold tree)")
	}
	if st.BBox.IsEmpty() {
		t.Errorf("BBox empty, want the encapsulated piece-tree bbox")
	}

	// A non-ring chunk -> nil.
	nonRing := level.ChunkPos{1 << 20, 1 << 20}
	if got := gen.GenerateStarts(seed, nonRing, sampler, biomeAt); got != nil {
		t.Errorf("non-ring chunk produced %d starts, want nil", len(got))
	}
}

type stubSurfaceSampler struct{ y int }

func (s stubSurfaceSampler) SampleSurfaceY(wx, wz int) int { return s.y }
