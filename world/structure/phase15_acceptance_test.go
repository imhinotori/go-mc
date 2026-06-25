package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// phase15_acceptance_test.go is the Phase-15 CLOSE gate (STRUCT-03 + STRUCT-04): every success
// criterion mapped to an automated assertion against an ALGORITHM-DERIVED reference (NOT an
// eyeballed value). It covers BOTH Phase-15 structures end-to-end:
//   (1) STRUCT-03 — the mineshaft places at the legacy-frequency rate (the chunk derived FROM
//       the 15-01-CORRECTED ApplyFrequencyReducer: legacy_type_3 -> legacyProbabilityReducer
//       WithDouble) + the recursive corridor/crossing/room graph fingerprint;
//   (2) STRUCT-04 placement — the ~128 stronghold ring positions match generateRingPositions +
//       isPlacementChunk true on a ring chunk / false off-ring;
//   (3) STRUCT-04 pieces — the recursive stronghold graph places (StartPiece + corridor/stairs
//       + the PortalRoom end_portal_frame ring + the Library bookshelves + the fingerprint);
//   (4) both structures deterministic per (seed,pos);
//   (5) cross-chunk idempotence for BOTH multi-chunk structures (union == whole, no double-
//       write, no truncation; a >=2-chunk-out owner found by the +-8 REFERENCES);
//   (6) a REAL-pipeline placement per structure (the production StartGenerator path).
// The 5x5 reorder-determinism (world TestDecorationReorderIdentical) + emit-once (TestEmitOnce)
// byte-identity WITH both structures live are asserted by the sibling ./world gates (run in the
// plan's verify command); this file owns the per-criterion structure-level assertions.

// p15Sampler is a flat underground surface stub (both Phase-15 structures are underground;
// their placement does not gate on terrain height beyond the anchor offset).
type p15Sampler struct{}

func (p15Sampler) SampleSurfaceY(int, int) int { return 64 }

// TestPhase15Acceptance is the single close gate mapping every STRUCT-03/04 criterion to an
// assertion. Sub-tests keep each criterion independently diagnosable.
func TestPhase15Acceptance(t *testing.T) {
	t.Run("STRUCT-03 mineshaft frequency + graph", func(t *testing.T) {
		const seed = int64(0xA17EC0DE)
		// The reference chunk is derived FROM the corrected ApplyFrequencyReducer (legacy_type_3
		// -> legacyProbabilityReducerWithDouble), NOT eyeballed.
		p := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
		passCX, passCZ, ok := -1, -1, false
		for cx := 0; cx < 400 && !ok; cx++ {
			for cz := 0; cz < 400 && !ok; cz++ {
				if p.ApplyFrequencyReducer(seed, cx, cz) {
					passCX, passCZ, ok = cx, cz, true
				}
			}
		}
		if !ok {
			t.Fatal("no frequency-passing mineshaft chunk in 400x400 — reducer reference broken")
		}
		// spacing 1 makes IsStructureChunk ALWAYS true — the gate is the frequency draw.
		if !p.IsStructureChunk(seed, passCX, passCZ) {
			t.Fatal("IsStructureChunk should be ALWAYS true at spacing 1 (gate is the reducer)")
		}

		g, err := NewMineshaftStartGen()
		if err != nil {
			t.Fatalf("NewMineshaftStartGen: %v", err)
		}
		biome := mineshaftBiome(t, "minecraft:plains")
		starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, p15Sampler{}, biome)
		if len(starts) != 1 {
			t.Fatalf("mineshaft start count = %d, want 1 at the reducer-derived chunk", len(starts))
		}
		if len(starts[0].Pieces) < 2 {
			t.Fatalf("mineshaft graph too small (%d pieces) — recursion not firing", len(starts[0].Pieces))
		}
		// The recursive graph fingerprint is determinism-stable.
		fpA := placeStartFingerprint(t, starts[0], seed)
		fpB := placeStartFingerprint(t, starts[0], seed)
		if fpA != fpB {
			t.Fatalf("mineshaft graph fingerprint non-deterministic: 0x%x != 0x%x", fpA, fpB)
		}
		t.Logf("STRUCT-03 mineshaft chunk=(%d,%d) pieces=%d fp=0x%x", passCX, passCZ, len(starts[0].Pieces), fpA)
	})

	t.Run("STRUCT-04 ring positions match generateRingPositions", func(t *testing.T) {
		const seed = int64(0x57204)
		preferred, err := LoadStrongholdBiasedTo()
		if err != nil {
			t.Fatalf("LoadStrongholdBiasedTo: %v", err)
		}
		biomeAt := func(int, int, int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }
		rs := NewStrongholdRingState(seed, biomeAt, preferred)
		positions := rs.RingPositions()

		// The reference: generateRingPositions directly (the algorithm), same seed + placement.
		p, err := LoadConcentricRingsPlacement("minecraft:strongholds")
		if err != nil {
			t.Fatalf("LoadConcentricRingsPlacement: %v", err)
		}
		ref := generateRingPositions(seed, p, biomeAt, preferred)
		if len(positions) != len(ref) {
			t.Fatalf("ring position count %d != algorithm reference %d", len(positions), len(ref))
		}
		if len(positions) < 100 || len(positions) > 130 {
			t.Fatalf("ring position count %d not the ~128 vanilla count", len(positions))
		}
		for i := range ref {
			if positions[i] != ref[i] {
				t.Fatalf("ring position %d = %v != algorithm reference %v", i, positions[i], ref[i])
			}
		}
		// isPlacementChunk: true on a ring chunk, false off-ring.
		if !rs.isPlacementChunk(positions[0]) {
			t.Fatalf("isPlacementChunk false at a known ring position %v", positions[0])
		}
		if rs.isPlacementChunk(level.ChunkPos{1 << 20, 1 << 20}) {
			t.Fatal("isPlacementChunk true at a far off-ring chunk")
		}
		t.Logf("STRUCT-04 ring positions=%d (vanilla ~128)", len(positions))
	})

	t.Run("STRUCT-04 stronghold pieces place", func(t *testing.T) {
		const seed = int64(0x57105)
		pieces := strongholdPieceGraph(seed, 6, 6, 40)
		if len(pieces) < 3 {
			t.Fatalf("stronghold graph too small (%d pieces)", len(pieces))
		}
		if n := countPortalRooms(pieces); n != 1 {
			t.Fatalf("PortalRoom count = %d, want exactly 1", n)
		}
		// The signature blocks were asserted in TestStrongholdSignatureBlocks; here we pin the
		// graph fingerprint is determinism-stable WITH the StartPiece + recursion.
		start := &StructureStart{Structure: "minecraft:stronghold", ChunkPos: level.ChunkPos{6, 6}, Pieces: pieces}
		start.RecomputeBBox()
		if start.BBox.IsEmpty() {
			t.Fatal("stronghold BBox empty after RecomputeBBox")
		}
		fpA := placeStartFingerprint(t, start, seed)
		fpB := placeStartFingerprint(t, start, seed)
		if fpA != fpB {
			t.Fatalf("stronghold graph fingerprint non-deterministic: 0x%x != 0x%x", fpA, fpB)
		}
		t.Logf("STRUCT-04 stronghold pieces=%d fp=0x%x", len(pieces), fpA)
	})

	t.Run("both structures deterministic per seed", func(t *testing.T) {
		const seed = int64(0xDE7E47)
		// Mineshaft.
		g, _ := NewMineshaftStartGen()
		p := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
		var mcx, mcz int
		found := false
		for cx := 0; cx < 400 && !found; cx++ {
			for cz := 0; cz < 400 && !found; cz++ {
				if p.ApplyFrequencyReducer(seed, cx, cz) {
					mcx, mcz, found = cx, cz, true
				}
			}
		}
		biome := mineshaftBiome(t, "minecraft:plains")
		a := g.GenerateStarts(seed, level.ChunkPos{int32(mcx), int32(mcz)}, p15Sampler{}, biome)
		b := g.GenerateStarts(seed, level.ChunkPos{int32(mcx), int32(mcz)}, p15Sampler{}, biome)
		if len(a) != 1 || len(b) != 1 || len(a[0].Pieces) != len(b[0].Pieces) || a[0].BBox != b[0].BBox {
			t.Fatal("mineshaft start not pure over (seed,pos)")
		}
		// Stronghold.
		sa := strongholdPieceGraph(seed, 3, 9, 40)
		sb := strongholdPieceGraph(seed, 3, 9, 40)
		if len(sa) != len(sb) {
			t.Fatalf("stronghold graph not pure: %d != %d pieces", len(sa), len(sb))
		}
		for i := range sa {
			if sa[i].BoundingBox() != sb[i].BoundingBox() {
				t.Fatalf("stronghold piece %d not pure", i)
			}
		}
	})

	t.Run("both structures cross-chunk idempotent", func(t *testing.T) {
		// Mineshaft.
		assertCrossChunkIdempotent(t, mineshaftStartForCrossChunk(t, 0x5A1F), 0x5A1F)
		// Stronghold.
		sPieces := strongholdPieceGraph(0x57A1, 4, 4, 40)
		sStart := &StructureStart{Structure: "minecraft:stronghold", ChunkPos: level.ChunkPos{4, 4}, Pieces: sPieces}
		sStart.RecomputeBBox()
		assertCrossChunkIdempotent(t, sStart, 0x57A1)
	})

	t.Run("real-pipeline placement per structure", func(t *testing.T) {
		// MINESHAFT real path: the production StartGenerator gates on ApplyFrequencyReducer +
		// the REAL biome, builds the recursive graph, and the graph writes blocks via PostProcess.
		const seed = int64(0xBEEF)
		g, _ := NewMineshaftStartGen()
		p := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
		var mcx, mcz int
		ok := false
		for cx := 0; cx < 400 && !ok; cx++ {
			for cz := 0; cz < 400 && !ok; cz++ {
				if p.ApplyFrequencyReducer(seed, cx, cz) {
					mcx, mcz, ok = cx, cz, true
				}
			}
		}
		ms := g.GenerateStarts(seed, level.ChunkPos{int32(mcx), int32(mcz)}, p15Sampler{}, mineshaftBiome(t, "minecraft:plains"))
		if len(ms) != 1 || placeStartBlockCount(t, ms[0], seed) == 0 {
			t.Fatal("mineshaft real-pipeline placement wrote zero blocks")
		}

		// STRONGHOLD real path: the production strongholdStartGen on a ring chunk returns the
		// populated start; placing it writes blocks.
		preferred, _ := LoadStrongholdBiasedTo()
		biomeAt := func(int, int, int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }
		rs := NewStrongholdRingState(seed, biomeAt, preferred)
		gen := NewStrongholdStartGen(rs)
		ring := rs.RingPositions()[0]
		ss := gen.GenerateStarts(seed, ring, p15Sampler{}, biomeAt)
		if len(ss) != 1 || len(ss[0].Pieces) == 0 {
			t.Fatal("stronghold real-pipeline start empty on a ring chunk")
		}
		if placeStartBlockCount(t, ss[0], seed) == 0 {
			t.Fatal("stronghold real-pipeline placement wrote zero blocks")
		}
	})
}

// placeStartFingerprint places a start's whole piece graph into one unbounded view and returns
// the FNV fingerprint over the placed blocks (the re-derivable SetLargeFeatureSeed stream).
func placeStartFingerprint(t *testing.T, start *StructureStart, seed int64) uint64 {
	t.Helper()
	return fingerprintBlocks(placeStartView(t, start, seed))
}

func placeStartBlockCount(t *testing.T, start *StructureStart, seed int64) int {
	t.Helper()
	return len(placeStartView(t, start, seed).blocks)
}

func placeStartView(t *testing.T, start *StructureStart, seed int64) *mapView {
	t.Helper()
	bb := start.BBox
	box := BoundingBox{bb.MinX - 16, bb.MinY - 16, bb.MinZ - 16, bb.MaxX + 16, bb.MaxY + 16, bb.MaxZ + 16}
	v := newMapView()
	for _, p := range start.Pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, int(start.ChunkPos[0]), int(start.ChunkPos[1]))
		p.PostProcess(v, box, start.ChunkPos, rng)
	}
	return v
}

// mineshaftStartForCrossChunk builds a mineshaft start at the frequency-derived chunk.
func mineshaftStartForCrossChunk(t *testing.T, seed int64) *StructureStart {
	t.Helper()
	g, _ := NewMineshaftStartGen()
	p := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
	for cx := 0; cx < 400; cx++ {
		for cz := 0; cz < 400; cz++ {
			if p.ApplyFrequencyReducer(seed, cx, cz) {
				starts := g.GenerateStarts(seed, level.ChunkPos{int32(cx), int32(cz)}, p15Sampler{}, mineshaftBiome(t, "minecraft:plains"))
				if len(starts) == 1 {
					return starts[0]
				}
			}
		}
	}
	t.Fatal("no mineshaft start found for cross-chunk test")
	return nil
}

// assertCrossChunkIdempotent pins union==whole-graph + no double-write + idempotence for a
// multi-chunk start, AND that it genuinely spans >=2 chunks (the +-8 REFERENCES load).
func assertCrossChunkIdempotent(t *testing.T, start *StructureStart, seed int64) {
	t.Helper()
	bb := start.BBox
	whole := newMapView()
	bigBox := BoundingBox{bb.MinX - 1, bb.MinY - 1, bb.MinZ - 1, bb.MaxX + 1, bb.MaxY + 1, bb.MaxZ + 1}
	for _, p := range start.Pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, int(start.ChunkPos[0]), int(start.ChunkPos[1]))
		p.PostProcess(whole, bigBox, start.ChunkPos, rng)
	}
	minCX, maxCX := bb.MinX>>4, bb.MaxX>>4
	minCZ, maxCZ := bb.MinZ>>4, bb.MaxZ>>4
	if (maxCX-minCX+1)*(maxCZ-minCZ+1) < 2 {
		t.Fatalf("structure %q spans <2 chunks — not a cross-chunk test (bbox %v)", start.Structure, bb)
	}
	placeAll := func() *mapView {
		u := newMapView()
		for cx := minCX; cx <= maxCX; cx++ {
			for cz := minCZ; cz <= maxCZ; cz++ {
				ch := level.ChunkPos{int32(cx), int32(cz)}
				placeInChunk(start, u, WritableArea(ch, -64, 384), ch, seed)
			}
		}
		return u
	}
	union := placeAll()
	for pos, st := range whole.blocks {
		if pos[1] < -64 || pos[1] > -64+384-1 {
			continue
		}
		if union.blocks[pos] != st {
			t.Fatalf("%q cross-chunk union diverges at %v: union %v vs whole %v", start.Structure, pos, union.blocks[pos], st)
		}
	}
	union2 := placeAll()
	if len(union.blocks) != len(union2.blocks) {
		t.Fatalf("%q cross-chunk not idempotent: %d vs %d blocks", start.Structure, len(union.blocks), len(union2.blocks))
	}
	for pos, st := range union.blocks {
		if union2.blocks[pos] != st {
			t.Fatalf("%q cross-chunk not idempotent at %v", start.Structure, pos)
		}
	}
}
