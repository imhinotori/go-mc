package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// phase16_acceptance_test.go is the Phase-16 CLOSE gate (STRUCT-05 + STRUCT-06): every success
// criterion mapped to an automated assertion against an ALGORITHM-DERIVED reference (NOT an
// eyeballed value), plus the all-structures-place pass that asserts EVERY v2 structure lands at
// its algorithm-derived chunk. It mirrors phase15_acceptance_test.go's shape exactly.
//
// TestPhase16Acceptance maps the 5 Phase-16 criteria:
//   (1) .nbt template ROUND-TRIP: a known village .nbt parses to its expected size + palette +
//       block count + the placed-block fingerprint (16-01's contract re-asserted at the
//       acceptance level).
//   (2) the jigsaw Placer TERMINATES + BOUNDS: a real village assembles with a bounded piece
//       count, no two INDEPENDENT pieces' boxes overlap (the only strict-overlaps are children
//       deliberately folding back INTO their parent's box — the jar's localFree case), every
//       piece box within max_distance_from_center (80) of the start center, and the BFS depth
//       never exceeds size+1 (terminators at the boundary).
//   (3) a village PLACES: the salt-10387312 algorithm-derived expected chunk for a fixed seed +
//       the deterministic village fingerprint (stable across two assemblies).
//   (4) CROSS-CHUNK idempotence: a village spanning >=2 chunks; the union of per-chunk
//       placeInChunk slices == the whole placement, no double-write (reused via the shared
//       assertCrossChunkIdempotent helper).
//   (5) the BIOME gate is LOAD-BEARING: an out-of-biome (ocean) origin -> NO village start.
//
// TestAllStructuresPlace asserts, in ONE pass, that every v2 structure type appears at its
// algorithm-derived chunk with its expected fingerprint: desert pyramid + jungle temple + igloo
// + swamp hut + mineshaft + stronghold + the 5 village biome variants.
//
// The 5x5 reorder-determinism (world TestDecorationReorderIdentical) + emit-once (TestEmitOnce)
// byte-identity WITH villages live are asserted by the sibling ./world gates (run in the plan's
// verify command); this file owns the per-criterion structure-level assertions.

// villageAcceptSeed/Chunk: the fixed seed whose plains village lands at the ALGORITHM-DERIVED
// anchor chunk (15,2) — verified via IsStructureChunk(salt 10387312, spacing 34, separation 8),
// NOT eyeballed (see village_test.go). The flat sampler + forced plains biome make the placement
// router-independent + deterministic (the 14-02/15-03 fixed-oracle pattern).
const (
	villageAcceptSeed   = int64(0)
	villageAcceptChunkX = 15
	villageAcceptChunkZ = 2
)

// village max_distance_from_center (the structure JSON `max_distance_from_center`) + size.
const (
	villageMaxDistance = 80
	villageSize        = 6
)

// TestPhase16Acceptance is the single close gate mapping every STRUCT-05/06 criterion to an
// assertion. Sub-tests keep each criterion independently diagnosable.
func TestPhase16Acceptance(t *testing.T) {
	t.Run("STRUCT-05 .nbt template round-trip", func(t *testing.T) {
		// A known village .nbt parses to its expected size, palette length, and block count
		// (CFR StructureTemplate.load) — the 16-01 contract at the acceptance level.
		tmpl, err := LoadTemplate(plainsSmallHouse1)
		if err != nil {
			t.Fatalf("LoadTemplate(%s): %v", plainsSmallHouse1, err)
		}
		if tmpl.Size != [3]int{7, 7, 7} {
			t.Fatalf("template Size = %v; want [7 7 7]", tmpl.Size)
		}
		if len(tmpl.palette) != 24 {
			t.Fatalf("template palette len = %d; want 24", len(tmpl.palette))
		}
		if len(tmpl.blocks) != 343 {
			t.Fatalf("template blocks len = %d; want 343", len(tmpl.blocks))
		}
		// The placed-block fingerprint under (origin=0, NONE) is determinism-stable (a parser/
		// placer/palette regression flips it).
		v := newMapView()
		rng := levelgen.NewLegacyRandomSource(1)
		tmpl.PlaceInWorld(v, Pos{0, 0, 0}, RotNone, MirrorNone, 0, 0, nil, bigBox, rng)
		count, fp := fingerprint(v)
		const wantCount = 343
		const wantFP = uint64(326779301212395908)
		if count != wantCount || fp != wantFP {
			t.Fatalf(".nbt round-trip fingerprint = (%d, %d), want pinned (%d, %d)", count, fp, wantCount, wantFP)
		}
		t.Logf("STRUCT-05 .nbt round-trip: %s size=%v palette=%d blocks=%d fp=%d", plainsSmallHouse1, tmpl.Size, len(tmpl.palette), count, fp)
	})

	t.Run("STRUCT-05 Placer terminates + bounds", func(t *testing.T) {
		// Assemble a real village; assert the THREE jigsaw bounds hold (the load-bearing
		// contract — drop any one and the village grows forever or overlaps itself).
		g := newVillageGen(t)
		pos := level.ChunkPos{villageAcceptChunkX, villageAcceptChunkZ}
		starts := g.GenerateStarts(villageAcceptSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
		if len(starts) != 1 {
			t.Fatalf("village did not place at the anchor (%d starts)", len(starts))
		}
		st := starts[0]

		// BOUNDED piece count: a real village places ~10-150 pieces, never the 1000-piece
		// defensive cap (T-16-03 — the Placer terminated well within bounds).
		if len(st.Pieces) < 2 {
			t.Fatalf("village has %d pieces — the Placer did not expand", len(st.Pieces))
		}
		if len(st.Pieces) >= jigsawTotalPieceCap {
			t.Fatalf("village hit the %d-piece defensive cap — the Placer did not terminate via the bounds", jigsawTotalPieceCap)
		}

		// The 80-radius bound + the collision bound: every piece box must be within
		// max_distance_from_center of the start center (bound #2), and no two INDEPENDENT
		// pieces may strict-overlap (bound #3 — the VoxelShape contract). The ONLY allowed
		// strict-overlaps are children folding back INTO a parent's XZ footprint (the jar's
		// localFree case — small filler/decoration pieces inside a building).
		bb := st.BBox
		centerX := (bb.MaxX + bb.MinX) / 2
		centerZ := (bb.MaxZ + bb.MinZ) / 2
		for i := range st.Pieces {
			pb := st.Pieces[i].BoundingBox()
			// max-distance bound (horizontal Chebyshev, the jar's MaxDistance.horizontal).
			for _, c := range [][2]int{{pb.MinX, pb.MinZ}, {pb.MaxX, pb.MaxZ}, {pb.MinX, pb.MaxZ}, {pb.MaxX, pb.MinZ}} {
				if d := chebyshev(c[0]-centerX, c[1]-centerZ); d > villageMaxDistance {
					t.Fatalf("piece %d corner (%d,%d) is %d from center (%d,%d) — exceeds max_distance %d",
						i, c[0], c[1], d, centerX, centerZ, villageMaxDistance)
				}
			}
			// no INDEPENDENT-sibling strict overlap.
			for j := i + 1; j < len(st.Pieces); j++ {
				ob := st.Pieces[j].BoundingBox()
				if !strictOverlap(pb, ob) {
					continue
				}
				// allowed ONLY if one is fully inside the other's XZ footprint (a fold-back
				// child inside its parent building — the jar's localFree expansion).
				if xzContains(pb, ob) || xzContains(ob, pb) {
					continue
				}
				t.Fatalf("independent pieces %d and %d strict-overlap (not a fold-back): %+v vs %+v", i, j, pb, ob)
			}
		}

		// DEPTH bound (#1): the BFS depth never exceeds size (a piece at depth==size attaches
		// only fallback/terminators; its children get depth size+1 but are NOT enqueued — so
		// the max genDepth is size+1, the boundary terminators).
		maxDepth := 0
		for _, p := range st.Pieces {
			if pp, ok := p.(*PoolElementStructurePiece); ok && pp.genDepth > maxDepth {
				maxDepth = pp.genDepth
			}
		}
		if maxDepth > villageSize+1 {
			t.Fatalf("village BFS depth %d exceeds size+1 (%d) — the depth bound (#1) is broken", maxDepth, villageSize+1)
		}
		t.Logf("STRUCT-05 Placer bounds: pieces=%d maxDepth=%d (size=%d) center=(%d,%d)", len(st.Pieces), maxDepth, villageSize, centerX, centerZ)
	})

	t.Run("STRUCT-05 a village places + deterministic fingerprint", func(t *testing.T) {
		// The anchor MUST be the algorithm's potential structure chunk (the placement decision),
		// derived from the placement math, not eyeballed.
		p := RandomSpreadStructurePlacement{Spacing: 34, Separation: 8, Salt: 10387312, SpreadType: SpreadLinear}
		if !p.IsStructureChunk(villageAcceptSeed, villageAcceptChunkX, villageAcceptChunkZ) {
			t.Fatalf("anchor (%d,%d) is not the algorithm's structure chunk for seed %d", villageAcceptChunkX, villageAcceptChunkZ, villageAcceptSeed)
		}

		g := newVillageGen(t)
		pos := level.ChunkPos{villageAcceptChunkX, villageAcceptChunkZ}
		fpA := placeVillageFingerprint(t, g, pos)
		fpB := placeVillageFingerprint(t, g, pos)
		if fpA != fpB {
			t.Fatalf("village fingerprint non-deterministic: 0x%x != 0x%x", fpA, fpB)
		}
		// Pinned to the algorithm-derived placement (a Placer/template/RNG regression flips it).
		const wantFP = uint64(0xcd3845c885976c04)
		if fpA != wantFP {
			t.Fatalf("village fingerprint = 0x%x, want pinned 0x%x", fpA, wantFP)
		}
		t.Logf("STRUCT-05 village places at chunk (%d,%d) fp=0x%x", villageAcceptChunkX, villageAcceptChunkZ, fpA)
	})

	t.Run("STRUCT-05 cross-chunk idempotent", func(t *testing.T) {
		g := newVillageGen(t)
		pos := level.ChunkPos{villageAcceptChunkX, villageAcceptChunkZ}
		starts := g.GenerateStarts(villageAcceptSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
		if len(starts) != 1 {
			t.Fatalf("village did not place (%d starts)", len(starts))
		}
		assertCrossChunkIdempotent(t, starts[0], villageAcceptSeed)
	})

	t.Run("STRUCT-05 biome gate load-bearing", func(t *testing.T) {
		// An out-of-biome (ocean) origin is in NO village variant's has_structure set -> no
		// village start. Dropping the gate would place a village in the ocean.
		g := newVillageGen(t)
		pos := level.ChunkPos{villageAcceptChunkX, villageAcceptChunkZ}
		oceanType := villageBiomeType(t, "minecraft:ocean")
		oceanBiomeAt := func(int, int, int) levelbiome.Type { return oceanType }
		out := g.GenerateStarts(villageAcceptSeed, pos, flatVillageSampler{}, oceanBiomeAt)
		if len(out) != 0 {
			t.Fatalf("out-of-biome (ocean) origin produced %d village starts; want 0 (accept-by-default leak)", len(out))
		}
		t.Log("STRUCT-05 biome gate load-bearing: ocean origin -> 0 villages")
	})
}

// TestAllStructuresPlace asserts, in ONE pass, that EVERY v2 structure type places at its
// algorithm-derived chunk with its expected fingerprint: desert pyramid + jungle temple + igloo
// + swamp hut + mineshaft + stronghold + the 5 village biome variants. The expected chunk is
// derived FROM the placement algorithm (per-salt PotentialStructureChunk / the legacy-frequency
// reducer / the concentric-ring positions), never eyeballed; the fingerprint is the completeness
// oracle (a placement/geometry/RNG regression in any one structure flips its hash).
func TestAllStructuresPlace(t *testing.T) {
	// --- the four scattered temples (the 14-03 algorithm-derived anchors + pinned fingerprints) ---
	type temple struct {
		name      string
		newGen    func() (StartGenerator, error)
		seed      int64
		cx, cz    int
		sampler   SurfaceSampler
		biome     BiomeAt
		wantCount int
		wantHash  uint64
	}
	temples := []temple{
		{"desert_pyramid", NewDesertPyramidStartGen, desertSeed, desertChunkX, desertChunkZ, desertSurfaceSampler{}, desertBiomeAt, 57050, 0x02570922c9bdc1bf},
		{"jungle_temple", NewJungleTempleStartGen, jungleSeed, jungleChunkX, jungleChunkZ, jungleSurfaceSampler{}, jungleBiomeAt, 1746, 0xf50b9fb8be352499},
		{"igloo", NewIglooStartGen, iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ, iglooSurfaceSampler{}, snowyBiomeAt, 357, 0x8b891d28bdfa0c7b},
		{"swamp_hut", NewSwampHutStartGen, swampSeed, swampChunkX, swampChunkZ, swampSurfaceSampler{}, swampBiomeAt, 640, 0xa2cda652a4805635},
	}
	for _, tm := range temples {
		t.Run(tm.name, func(t *testing.T) {
			// the expected chunk IS its own region's potential start chunk (algorithm-derived).
			placement := RandomSpreadStructurePlacement{Spacing: 32, Separation: 8, Salt: templeSalt(tm.name), SpreadType: SpreadLinear}
			if pot := placement.PotentialStructureChunk(tm.seed, tm.cx, tm.cz); pot != (level.ChunkPos{int32(tm.cx), int32(tm.cz)}) {
				t.Fatalf("%s: (%d,%d) is not its region's potential start chunk (got %v)", tm.name, tm.cx, tm.cz, pot)
			}
			gen, err := tm.newGen()
			if err != nil {
				t.Fatal(err)
			}
			starts := gen.GenerateStarts(tm.seed, level.ChunkPos{int32(tm.cx), int32(tm.cz)}, tm.sampler, tm.biome)
			if len(starts) != 1 {
				t.Fatalf("%s: expected 1 start, got %d", tm.name, len(starts))
			}
			view := newMapView()
			rng := levelgen.NewWorldgenRandom(0)
			rng.SetLargeFeatureSeed(tm.seed, tm.cx, tm.cz)
			for _, pc := range starts[0].Pieces {
				pc.PostProcess(view, fullBox(), level.ChunkPos{int32(tm.cx), int32(tm.cz)}, rng)
			}
			count, hash := fingerprint(view)
			if count != tm.wantCount || hash != tm.wantHash {
				t.Fatalf("%s: fingerprint = (%d, %#016x), want (%d, %#016x)", tm.name, count, hash, tm.wantCount, tm.wantHash)
			}
			t.Logf("%s places at (%d,%d): count=%d fp=%#016x", tm.name, tm.cx, tm.cz, count, hash)
		})
	}

	// --- the mineshaft (the 15-01 legacy-frequency reducer chunk + deterministic graph) ---
	t.Run("mineshaft", func(t *testing.T) {
		const seed = int64(0xA17EC0DE)
		mp := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
		cx, cz, ok := -1, -1, false
		for x := 0; x < 400 && !ok; x++ {
			for z := 0; z < 400 && !ok; z++ {
				if mp.ApplyFrequencyReducer(seed, x, z) {
					cx, cz, ok = x, z, true
				}
			}
		}
		if !ok {
			t.Fatal("no frequency-passing mineshaft chunk in 400x400 — reducer reference broken")
		}
		g, err := NewMineshaftStartGen()
		if err != nil {
			t.Fatal(err)
		}
		starts := g.GenerateStarts(seed, level.ChunkPos{int32(cx), int32(cz)}, p15Sampler{}, mineshaftBiome(t, "minecraft:plains"))
		if len(starts) != 1 || len(starts[0].Pieces) < 2 {
			t.Fatalf("mineshaft did not place a multi-piece graph at the reducer chunk (%d,%d)", cx, cz)
		}
		fpA := placeStartFingerprint(t, starts[0], seed)
		fpB := placeStartFingerprint(t, starts[0], seed)
		if fpA != fpB {
			t.Fatalf("mineshaft graph fingerprint non-deterministic: 0x%x != 0x%x", fpA, fpB)
		}
		if placeStartBlockCount(t, starts[0], seed) == 0 {
			t.Fatal("mineshaft placed zero blocks")
		}
		t.Logf("mineshaft places at reducer chunk (%d,%d): pieces=%d fp=0x%x", cx, cz, len(starts[0].Pieces), fpA)
	})

	// --- the stronghold (the concentric-ring algorithm chunk + recursive graph) ---
	t.Run("stronghold", func(t *testing.T) {
		const seed = int64(0x57105)
		preferred, err := LoadStrongholdBiasedTo()
		if err != nil {
			t.Fatal(err)
		}
		biomeAt := func(int, int, int) levelbiome.Type { return biomeTypeOf(t, "minecraft:plains") }
		rs := NewStrongholdRingState(seed, biomeAt, preferred)
		ring := rs.RingPositions()[0]
		gen := NewStrongholdStartGen(rs)
		starts := gen.GenerateStarts(seed, ring, p15Sampler{}, biomeAt)
		if len(starts) != 1 || len(starts[0].Pieces) < 3 {
			t.Fatalf("stronghold did not place a graph at the ring chunk %v", ring)
		}
		if n := countPortalRooms(starts[0].Pieces); n != 1 {
			t.Fatalf("stronghold PortalRoom count = %d, want exactly 1", n)
		}
		fpA := placeStartFingerprint(t, starts[0], seed)
		fpB := placeStartFingerprint(t, starts[0], seed)
		if fpA != fpB {
			t.Fatalf("stronghold graph fingerprint non-deterministic: 0x%x != 0x%x", fpA, fpB)
		}
		t.Logf("stronghold places at ring chunk %v: pieces=%d fp=0x%x", ring, len(starts[0].Pieces), fpA)
	})

	// --- the 5 village biome variants (each at the salt-10387312 anchor chunk (15,2), forced
	// into its own biome so the placement is router-independent + the exact variant is pinned) ---
	t.Run("villages", func(t *testing.T) {
		g := newVillageGen(t)
		pos := level.ChunkPos{villageAcceptChunkX, villageAcceptChunkZ}
		// the anchor IS its region's potential start chunk (algorithm-derived).
		p := RandomSpreadStructurePlacement{Spacing: 34, Separation: 8, Salt: 10387312, SpreadType: SpreadLinear}
		if !p.IsStructureChunk(villageAcceptSeed, villageAcceptChunkX, villageAcceptChunkZ) {
			t.Fatalf("village anchor (%d,%d) is not the algorithm's structure chunk", villageAcceptChunkX, villageAcceptChunkZ)
		}
		variants := []struct {
			id, biome string
			wantCount int
			wantFP    uint64
		}{
			{"minecraft:village_plains", "minecraft:plains", 5768, 0xcd3845c885976c04},
			{"minecraft:village_desert", "minecraft:desert", 3588, 0x433db86bbc8929c9},
			{"minecraft:village_savanna", "minecraft:savanna", 7444, 0x583d448a5a5d5334},
			{"minecraft:village_snowy", "minecraft:snowy_plains", 11851, 0x473a285f3db82b03},
			{"minecraft:village_taiga", "minecraft:taiga", 6278, 0xbd5888cd0b4c30ca},
		}
		for _, vv := range variants {
			t.Run(vv.id, func(t *testing.T) {
				bt := villageBiomeType(t, vv.biome)
				biomeAt := func(int, int, int) levelbiome.Type { return bt }
				starts := g.GenerateStarts(villageAcceptSeed, pos, flatVillageSampler{}, biomeAt)
				if len(starts) != 1 {
					t.Fatalf("%s: expected 1 start in biome %s, got %d", vv.id, vv.biome, len(starts))
				}
				if starts[0].Structure != vv.id {
					t.Fatalf("biome %s picked variant %q, want %q", vv.biome, starts[0].Structure, vv.id)
				}
				view := newMapView()
				for _, pc := range starts[0].Pieces {
					rng := levelgen.NewWorldgenRandom(0)
					rng.SetLargeFeatureSeed(villageAcceptSeed, villageAcceptChunkX, villageAcceptChunkZ)
					pc.PostProcess(view, fullBox(), pos, rng)
				}
				count, fp := fingerprint(view)
				if count != vv.wantCount || fp != vv.wantFP {
					t.Fatalf("%s: fingerprint = (%d, %#016x), want (%d, %#016x)", vv.id, count, fp, vv.wantCount, vv.wantFP)
				}
				t.Logf("%s places at (%d,%d): count=%d fp=%#016x", vv.id, villageAcceptChunkX, villageAcceptChunkZ, count, fp)
			})
		}
	})
}

// placeVillageFingerprint assembles the village at pos and returns the FNV fingerprint over its
// whole placed-block set (each piece re-seeded per the placeInChunk cross-chunk pattern).
func placeVillageFingerprint(t *testing.T, g StartGenerator, pos level.ChunkPos) uint64 {
	t.Helper()
	starts := g.GenerateStarts(villageAcceptSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
	if len(starts) != 1 {
		t.Fatalf("village did not place at %v (%d starts)", pos, len(starts))
	}
	view := newMapView()
	for _, p := range starts[0].Pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(villageAcceptSeed, int(pos[0]), int(pos[1]))
		p.PostProcess(view, fullBox(), pos, rng)
	}
	_, fp := fingerprint(view)
	return fp
}

// chebyshev returns the Chebyshev (max-axis) distance of a 2D offset — the jar's horizontal
// max_distance_from_center metric.
func chebyshev(dx, dz int) int {
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	if dz > dx {
		return dz
	}
	return dx
}

// xzContains reports whether inner's XZ footprint is fully inside outer's (the fold-back test:
// a child placed inside its parent building's footprint via the jar's localFree shape).
func xzContains(outer, inner BoundingBox) bool {
	return inner.MinX >= outer.MinX && inner.MaxX <= outer.MaxX &&
		inner.MinZ >= outer.MinZ && inner.MaxZ <= outer.MaxZ
}

// templeSalt maps a temple name to its placement salt (the 14-03 per-structure salts).
func templeSalt(name string) int {
	switch name {
	case "desert_pyramid":
		return desertPyramidSalt
	case "jungle_temple":
		return jungleTempleSalt
	case "igloo":
		return iglooSalt
	case "swamp_hut":
		return swampHutSalt
	}
	return 0
}
