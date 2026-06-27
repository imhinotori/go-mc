package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestStructuresPipelineAcceptance is the automated structures-pipeline acceptance gate (NO
// visual gate — Phase 16 owns that): each of the FOUR scattered temples (desert pyramid +
// jungle temple + igloo + swamp hut) lands at its EXPECTED chunk (derived per-salt from
// getPotentialStructureChunk, NOT eyeballed) ONLY when the origin biome is in its
// has_structure allow-set (a forced out-of-biome origin -> NO start, proving the biome gate is
// load-bearing) and with its FULL expected block fingerprint (the completeness oracle).
func TestStructuresPipelineAcceptance(t *testing.T) {
	type temple struct {
		name       string
		salt       int
		seed       int64
		cx, cz     int
		newGen     func() (StartGenerator, error)
		sampler    SurfaceSampler
		inBiome    BiomeAt
		outBiome   BiomeAt
		wantCount  int
		wantHash   uint64
		wantPieces int
	}

	temples := []temple{
		{
			name: "desert_pyramid", salt: desertPyramidSalt, seed: desertSeed, cx: desertChunkX, cz: desertChunkZ,
			newGen: NewDesertPyramidStartGen, sampler: desertSurfaceSampler{},
			inBiome: desertBiomeAt, outBiome: constBiome("minecraft:plains"),
			wantCount: 57050, wantHash: 0x02570922c9bdc1bf, wantPieces: 1,
		},
		{
			name: "jungle_temple", salt: jungleTempleSalt, seed: jungleSeed, cx: jungleChunkX, cz: jungleChunkZ,
			newGen: NewJungleTempleStartGen, sampler: jungleSurfaceSampler{},
			inBiome: jungleBiomeAt, outBiome: constBiome("minecraft:desert"),
			wantCount: 1746, wantHash: 0x471f867df2e73b40, wantPieces: 1,
		},
		{
			name: "igloo", salt: iglooSalt, seed: iglooBasementSeed, cx: iglooBasementChunkX, cz: iglooBasementChunkZ,
			newGen: NewIglooStartGen, sampler: iglooSurfaceSampler{},
			inBiome: snowyBiomeAt, outBiome: constBiome("minecraft:plains"),
			wantCount: 357, wantHash: 0x8b891d28bdfa0c7b, wantPieces: 3,
		},
		{
			name: "swamp_hut", salt: swampHutSalt, seed: swampSeed, cx: swampChunkX, cz: swampChunkZ,
			newGen: NewSwampHutStartGen, sampler: swampSurfaceSampler{},
			inBiome: swampBiomeAt, outBiome: constBiome("minecraft:plains"),
			wantCount: 640, wantHash: 0xa2cda652a4805635, wantPieces: 1,
		},
	}

	// Cross-check: the four salts are all distinct (a duplicate would desync a temple vs a
	// vanilla seed — Pitfall #3).
	seen := map[int]string{}
	for _, tm := range temples {
		if prev, ok := seen[tm.salt]; ok {
			t.Fatalf("salt %d shared by %s and %s (must be unique)", tm.salt, prev, tm.name)
		}
		seen[tm.salt] = tm.name
	}

	for _, tm := range temples {
		t.Run(tm.name, func(t *testing.T) {
			placement := RandomSpreadStructurePlacement{Spacing: 32, Separation: 8, Salt: tm.salt, SpreadType: SpreadLinear}

			// (1) The expected chunk is derived FROM the placement algorithm (per-salt), not
			// eyeballed.
			pot := placement.PotentialStructureChunk(tm.seed, tm.cx, tm.cz)
			if pot != (level.ChunkPos{int32(tm.cx), int32(tm.cz)}) {
				t.Fatalf("%s: seed %d region of (%d,%d) -> %v, want the owning chunk", tm.name, tm.seed, tm.cx, tm.cz, pot)
			}

			gen, err := tm.newGen()
			if err != nil {
				t.Fatal(err)
			}

			// (2) The biome gate is LOAD-BEARING: an out-of-biome origin -> NO start.
			out := gen.GenerateStarts(tm.seed, level.ChunkPos{int32(tm.cx), int32(tm.cz)}, tm.sampler, tm.outBiome)
			if len(out) != 0 {
				t.Fatalf("%s: an out-of-biome origin produced %d starts, want 0 (accept-by-default leaked)", tm.name, len(out))
			}

			// (3) In its correct biome the temple lands with its expected piece count + the full
			// block fingerprint.
			starts := gen.GenerateStarts(tm.seed, level.ChunkPos{int32(tm.cx), int32(tm.cz)}, tm.sampler, tm.inBiome)
			if len(starts) != 1 {
				t.Fatalf("%s: expected 1 start in-biome, got %d", tm.name, len(starts))
			}
			if len(starts[0].Pieces) != tm.wantPieces {
				t.Fatalf("%s: start has %d pieces, want %d", tm.name, len(starts[0].Pieces), tm.wantPieces)
			}

			view := newMapView()
			rng := levelgen.NewWorldgenRandom(0)
			rng.SetLargeFeatureSeed(tm.seed, tm.cx, tm.cz)
			for _, pc := range starts[0].Pieces {
				pc.PostProcess(view, fullBox(), level.ChunkPos{int32(tm.cx), int32(tm.cz)}, rng)
			}
			count, hash := fingerprint(view)
			if count != tm.wantCount || hash != tm.wantHash {
				t.Fatalf("%s: fingerprint = (%d, %#016x), want pinned (%d, %#016x)", tm.name, count, hash, tm.wantCount, tm.wantHash)
			}
		})
	}
}

// TestStructuresStartsPure: every temple's start decision is PURE over (seed,pos) — two
// GenerateStarts calls yield the identical owner/bbox/piece-count (the reproducible-world
// contract, incl. the igloo basement-present draw).
func TestStructuresStartsPure(t *testing.T) {
	cases := []struct {
		name   string
		newGen func() (StartGenerator, error)
		seed   int64
		cx, cz int
		samp   SurfaceSampler
		biome  BiomeAt
	}{
		{"desert", NewDesertPyramidStartGen, desertSeed, desertChunkX, desertChunkZ, desertSurfaceSampler{}, desertBiomeAt},
		{"jungle", NewJungleTempleStartGen, jungleSeed, jungleChunkX, jungleChunkZ, jungleSurfaceSampler{}, jungleBiomeAt},
		{"igloo", NewIglooStartGen, iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ, iglooSurfaceSampler{}, snowyBiomeAt},
		{"swamp", NewSwampHutStartGen, swampSeed, swampChunkX, swampChunkZ, swampSurfaceSampler{}, swampBiomeAt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gen, err := c.newGen()
			if err != nil {
				t.Fatal(err)
			}
			a := gen.GenerateStarts(c.seed, level.ChunkPos{int32(c.cx), int32(c.cz)}, c.samp, c.biome)
			b := gen.GenerateStarts(c.seed, level.ChunkPos{int32(c.cx), int32(c.cz)}, c.samp, c.biome)
			if len(a) != len(b) {
				t.Fatalf("%s: non-deterministic start count %d vs %d", c.name, len(a), len(b))
			}
			if len(a) == 1 && (a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox || len(a[0].Pieces) != len(b[0].Pieces)) {
				t.Fatalf("%s: start not pure over (seed,pos): %+v vs %+v", c.name, a[0], b[0])
			}
		})
	}
}

// TestStructuresCrossChunkIdempotent: a chunk-spanning temple (the desert pyramid, footprint 21
// wide) placed from EACH overlapping chunk's writable box (via placeInChunk) writes ONLY that
// chunk's slice; the union equals the whole-temple placement with NO double-writes/truncation
// (Pitfall #2 — the 14-02 cross-chunk clip re-pinned with all four temples live). The jungle
// temple (12 wide) + igloo also span chunks and reuse the same clip.
func TestStructuresCrossChunkIdempotent(t *testing.T) {
	cases := []struct {
		name  string
		whole func(t *testing.T) (*mapView, *StructureStart)
		seed  int64
	}{
		{"desert", func(t *testing.T) (*mapView, *StructureStart) {
			v, s, _ := placeWholePyramid(t)
			return v, s
		}, desertSeed},
		{"jungle", func(t *testing.T) (*mapView, *StructureStart) {
			v, s, _ := placeWholeJungleTemple(t)
			return v, s
		}, jungleSeed},
		{"igloo", func(t *testing.T) (*mapView, *StructureStart) {
			return placeIgloo(t, iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ)
		}, iglooBasementSeed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			whole, start := c.whole(t)
			bb := start.BBox
			minCX, maxCX := bb.MinX>>4, bb.MaxX>>4
			minCZ, maxCZ := bb.MinZ>>4, bb.MaxZ>>4

			union := newMapView()
			for cx := minCX; cx <= maxCX; cx++ {
				for cz := minCZ; cz <= maxCZ; cz++ {
					ch := level.ChunkPos{int32(cx), int32(cz)}
					box := WritableArea(ch, -64, 384)
					placeInChunk(start, union, box, ch, c.seed)
				}
			}

			if len(union.blocks) != len(whole.blocks) {
				t.Fatalf("%s: cross-chunk union has %d blocks, whole has %d (truncation/double-write)", c.name, len(union.blocks), len(whole.blocks))
			}
			for pos, st := range whole.blocks {
				if union.blocks[pos] != st {
					t.Fatalf("%s: cross-chunk union diverges at %v: %v vs whole %v", c.name, pos, union.blocks[pos], st)
				}
			}
		})
	}
}

// constBiome returns a BiomeAt that always reports the given biome id.
func constBiome(id string) BiomeAt {
	var t levelbiome.Type
	if err := t.UnmarshalText([]byte(id)); err != nil {
		panic("test: bad biome id " + id + ": " + err.Error())
	}
	return func(int, int, int) levelbiome.Type { return t }
}
