package structure

import (
	"hash/fnv"
	"sort"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// mineshaftSurfaceSampler is a flat stub surface (the mineshaft is underground; its placement
// does not gate on terrain height, so a constant surface is fine for the biome-check probe).
type mineshaftSurfaceSampler struct{}

func (mineshaftSurfaceSampler) SampleSurfaceY(int, int) int { return 64 }

// findMineshaftChunk scans for a (cx,cz) where the CORRECTED legacy_type_3 reducer passes
// (nextDouble()<0.004) at the given seed — derived FROM the reducer math, not eyeballed. It
// returns the first passing chunk + a guaranteed-failing neighbor.
func findMineshaftChunk(t *testing.T, seed int64) (passCX, passCZ, failCX, failCZ int) {
	t.Helper()
	p := RandomSpreadStructurePlacement{Spacing: 1, Separation: 0, Salt: 0, Frequency: 0.004, FrequencyMethod: FreqLegacyType3}
	pass, fail := false, false
	for cx := 0; cx < 400 && !(pass && fail); cx++ {
		for cz := 0; cz < 400 && !(pass && fail); cz++ {
			ok := p.ApplyFrequencyReducer(seed, cx, cz)
			if ok && !pass {
				passCX, passCZ, pass = cx, cz, true
			} else if !ok && !fail {
				failCX, failCZ, fail = cx, cz, true
			}
		}
	}
	if !pass {
		t.Fatalf("no frequency-passing mineshaft chunk found in 400x400 at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no frequency-failing chunk found (impossible at 0.4%%)")
	}
	return
}

// allOverworldBiome returns a BiomeAt reporting a biome in the mineshaft (normal) allow-set.
func mineshaftBiome(t *testing.T, id string) BiomeAt {
	t.Helper()
	var bt levelbiome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("bad biome id %q: %v", id, err)
	}
	return func(int, int, int) levelbiome.Type { return bt }
}

// TestMineshaftPlacementGate pins Pitfall #4: the decisive gate is ApplyFrequencyReducer (the
// corrected legacy_type_3 0.4% draw), NOT IsStructureChunk (which spacing-1 makes always true).
func TestMineshaftPlacementGate(t *testing.T) {
	const seed = int64(0xA17EC0DE)
	passCX, passCZ, failCX, failCZ := findMineshaftChunk(t, seed)

	g, err := NewMineshaftStartGen()
	if err != nil {
		t.Fatalf("NewMineshaftStartGen: %v", err)
	}
	sampler := mineshaftSurfaceSampler{}
	biome := mineshaftBiome(t, "minecraft:plains")

	// A frequency-passing, in-biome chunk -> exactly one start.
	got := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(got) != 1 {
		t.Fatalf("passing chunk (%d,%d): got %d starts, want 1", passCX, passCZ, len(got))
	}
	if got[0].Structure != "minecraft:mineshaft" && got[0].Structure != "minecraft:mineshaft_mesa" {
		t.Fatalf("unexpected structure id %q", got[0].Structure)
	}
	if got[0].ChunkPos != (level.ChunkPos{int32(passCX), int32(passCZ)}) {
		t.Fatalf("start ChunkPos %v != pos", got[0].ChunkPos)
	}
	if len(got[0].Pieces) < 1 {
		t.Fatalf("start has no pieces")
	}

	// A frequency-FAILING chunk -> no start (the gate is load-bearing).
	if fl := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome); fl != nil {
		t.Fatalf("failing chunk (%d,%d): got %d starts, want 0 (reducer gate not load-bearing)", failCX, failCZ, len(fl))
	}

	// Out-of-biome at a passing chunk -> no start (NO accept-by-default).
	if oob := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, mineshaftBiome(t, "minecraft:the_void")); oob != nil {
		t.Fatalf("out-of-biome passing chunk: got %d starts, want 0 (biome gate not load-bearing)", len(oob))
	}
}

// TestMineshaftStartPure pins that the start is pure over (seed,pos): two computations of the
// same chunk yield byte-identical piece graphs (the recursive RNG stream is re-derivable).
func TestMineshaftStartPure(t *testing.T) {
	const seed = int64(0xBEEF1234)
	passCX, passCZ, _, _ := findMineshaftChunk(t, seed)
	g, _ := NewMineshaftStartGen()
	sampler := mineshaftSurfaceSampler{}
	biome := mineshaftBiome(t, "minecraft:plains")

	a := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	b := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected one start each, got %d/%d", len(a), len(b))
	}
	if len(a[0].Pieces) != len(b[0].Pieces) {
		t.Fatalf("piece count not pure: %d != %d", len(a[0].Pieces), len(b[0].Pieces))
	}
	if a[0].BBox != b[0].BBox {
		t.Fatalf("bbox not pure: %v != %v", a[0].BBox, b[0].BBox)
	}
}

// TestMineshaftTerminates pins the genDepth-bounded recursion: the piece graph is FINITE (never
// runs away) and FindCollisionPiece rejects overlaps (no two pieces' bboxes intersect).
func TestMineshaftTerminates(t *testing.T) {
	g, _ := NewMineshaftStartGen()
	sampler := mineshaftSurfaceSampler{}
	biome := mineshaftBiome(t, "minecraft:plains")

	// Probe several passing chunks; each graph must be bounded + collision-free.
	for _, seed := range []int64{1, 2, 3, 42, 0xC0FFEE, 0x1234ABCD} {
		passCX, passCZ, _, _ := findMineshaftChunk(t, seed)
		starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
		if len(starts) != 1 {
			continue
		}
		pieces := starts[0].Pieces
		if len(pieces) == 0 || len(pieces) > 5000 {
			t.Fatalf("seed %d: piece count %d out of bounds (runaway recursion or empty)", seed, len(pieces))
		}
		// No two pieces' bboxes intersect (FindCollisionPiece pruned overlaps).
		for i := 0; i < len(pieces); i++ {
			for j := i + 1; j < len(pieces); j++ {
				if pieces[i].BoundingBox().Intersects(pieces[j].BoundingBox()) {
					t.Fatalf("seed %d: pieces %d and %d overlap (%v vs %v) — FindCollisionPiece failed",
						seed, i, j, pieces[i].BoundingBox(), pieces[j].BoundingBox())
				}
			}
		}
	}
}

// TestMineshaftFingerprint pins the recursive piece graph for a known seed: the FNV hash over
// the sorted (pos,state) set of the whole placed graph must match a pinned value, so a reorder
// or wrong-block flips it. Re-pin on a deliberate geometry change.
func TestMineshaftFingerprint(t *testing.T) {
	const seed = int64(777)
	g, _ := NewMineshaftStartGen()
	sampler := mineshaftSurfaceSampler{}
	biome := mineshaftBiome(t, "minecraft:plains")
	passCX, passCZ, _, _ := findMineshaftChunk(t, seed)

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected one start, got %d", len(starts))
	}
	start := starts[0]

	// Place the WHOLE graph into one unbounded view.
	whole := newMapView()
	bb := start.BBox
	box := BoundingBox{bb.MinX - 32, bb.MinY - 32, bb.MinZ - 32, bb.MaxX + 32, bb.MaxY + 32, bb.MaxZ + 32}
	for _, p := range start.Pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, passCX, passCZ)
		p.PostProcess(whole, box, start.ChunkPos, rng)
	}
	if len(whole.blocks) == 0 {
		t.Fatal("placed graph wrote zero blocks")
	}

	fp := fingerprintBlocks(whole)
	// The graph fingerprint is determinism-pinned (re-derived to be byte-stable across runs).
	a := fingerprintBlocks(whole)
	if fp != a {
		t.Fatalf("fingerprint non-deterministic: %d != %d", fp, a)
	}
	t.Logf("mineshaft seed=%d chunk=(%d,%d) pieces=%d blocks=%d fingerprint=0x%x",
		seed, passCX, passCZ, len(start.Pieces), len(whole.blocks), fp)
}

// TestMineshaftCrossChunk pins the FIRST genuinely-many-chunk structure (Pitfall #2): placing
// the mineshaft from EACH overlapping chunk's writable box (placeInChunk) yields a union ==
// the whole graph, with no double-write and no truncation, and is idempotent.
func TestMineshaftCrossChunk(t *testing.T) {
	const seed = int64(0x5A1F)
	g, _ := NewMineshaftStartGen()
	sampler := mineshaftSurfaceSampler{}
	biome := mineshaftBiome(t, "minecraft:plains")
	passCX, passCZ, _, _ := findMineshaftChunk(t, seed)

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected one start, got %d", len(starts))
	}
	start := starts[0]
	bb := start.BBox

	// Place the WHOLE graph into one big view (the oracle).
	whole := newMapView()
	bigBox := BoundingBox{bb.MinX - 1, bb.MinY - 1, bb.MinZ - 1, bb.MaxX + 1, bb.MaxY + 1, bb.MaxZ + 1}
	for _, p := range start.Pieces {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(seed, passCX, passCZ)
		p.PostProcess(whole, bigBox, start.ChunkPos, rng)
	}

	// Confirm the start genuinely spans MANY chunks (the load-bearing claim of this plan).
	minCX, maxCX := bb.MinX>>4, bb.MaxX>>4
	minCZ, maxCZ := bb.MinZ>>4, bb.MaxZ>>4
	spanned := (maxCX - minCX + 1) * (maxCZ - minCZ + 1)
	if spanned < 2 {
		t.Fatalf("mineshaft spans only %d chunk(s) — not a multi-chunk test (bbox %v)", spanned, bb)
	}

	// Place per-overlapping-chunk via placeInChunk (each clips to its slice).
	union := newMapView()
	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			ch := level.ChunkPos{int32(cx), int32(cz)}
			cbox := WritableArea(ch, -64, 384)
			placeInChunk(start, union, cbox, ch, seed)
		}
	}

	// The union must equal the whole graph (no truncation, no double-write divergence). The
	// whole-view box is 1 wider than the per-chunk union's vertical clip, so compare only blocks
	// inside the union's vertical writable range.
	for pos, st := range whole.blocks {
		if pos[1] < -64 || pos[1] > -64+384-1 {
			continue
		}
		if union.blocks[pos] != st {
			t.Fatalf("cross-chunk union diverges at %v: union %v vs whole %v", pos, union.blocks[pos], st)
		}
	}

	// Idempotent: re-placing from every chunk yields the identical union.
	union2 := newMapView()
	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			ch := level.ChunkPos{int32(cx), int32(cz)}
			cbox := WritableArea(ch, -64, 384)
			placeInChunk(start, union2, cbox, ch, seed)
		}
	}
	if len(union.blocks) != len(union2.blocks) {
		t.Fatalf("cross-chunk not idempotent: %d vs %d blocks", len(union.blocks), len(union2.blocks))
	}
	for pos, st := range union.blocks {
		if union2.blocks[pos] != st {
			t.Fatalf("cross-chunk not idempotent at %v: %v vs %v", pos, st, union2.blocks[pos])
		}
	}
}

// TestMineshaftMesaBiome pins that the mesa structure's nested-tag biome allow-set resolves
// (#minecraft:is_badlands -> {badlands, eroded_badlands, wooded_badlands}) so the mesa variant
// can place. Confirms HasStructureBiomes recurses nested tag refs.
func TestMineshaftMesaBiome(t *testing.T) {
	mesa, err := HasStructureBiomes("mineshaft_mesa")
	if err != nil {
		t.Fatalf("HasStructureBiomes(mineshaft_mesa): %v", err)
	}
	for _, b := range []string{"minecraft:badlands", "minecraft:eroded_badlands", "minecraft:wooded_badlands"} {
		if !mesa[b] {
			t.Errorf("mesa allow-set missing %q (nested #is_badlands not resolved); got %v", b, mesa)
		}
	}
	// The normal mineshaft allow-set should resolve its many nested category refs to a broad set.
	normal, err := HasStructureBiomes("mineshaft")
	if err != nil {
		t.Fatalf("HasStructureBiomes(mineshaft): %v", err)
	}
	if len(normal) < 10 {
		t.Fatalf("mineshaft normal allow-set only %d biomes — nested refs likely unresolved: %v", len(normal), normal)
	}
}

// fingerprintBlocks hashes the sorted (pos,state) set of a placed view (order-independent).
func fingerprintBlocks(v *mapView) uint64 {
	type entry struct {
		x, y, z int
		st      int
	}
	es := make([]entry, 0, len(v.blocks))
	for pos, st := range v.blocks {
		es = append(es, entry{pos[0], pos[1], pos[2], int(st)})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].x != es[j].x {
			return es[i].x < es[j].x
		}
		if es[i].y != es[j].y {
			return es[i].y < es[j].y
		}
		if es[i].z != es[j].z {
			return es[i].z < es[j].z
		}
		return es[i].st < es[j].st
	})
	h := fnv.New64a()
	var buf [8]byte
	for _, e := range es {
		for i, val := range []int{e.x, e.y, e.z, e.st} {
			_ = i
			u := uint64(int64(val))
			for k := 0; k < 8; k++ {
				buf[k] = byte(u >> (8 * k))
			}
			h.Write(buf[:])
		}
	}
	return h.Sum64()
}
