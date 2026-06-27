package world

import (
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/save/region"
	"github.com/imhinotori/sulfur/world/structure"
)

// worker_structure_persist_test.go — STRUCT-POLISH-04 gap closure: the RELOAD integration test.
// It proves the success criterion "computed StructureStarts persist to NBT so starts survive a
// reload WITHOUT recompute" end-to-end across the REAL runtime save/load seam:
//
//	generate a structure chunk (seeds the cache via Decorate) -> SerializeChunkData (WRITE path:
//	emits the `structures` compound) -> write to a region file -> a FRESH NoiseGenerator + Worker
//	loads it from disk (READ path: decodeAndSeed -> ReadChunkStructures seeds the fresh cache) ->
//	assert the start is served from NBT and the recompute spy was NOT hit for the owner.
//
// This is the runtime proof the 20-VERIFICATION gap demanded (the round-trip persistence functions
// were tested in isolation in world/structure, but had zero production callers until this wiring).

// recomputeSpyGen wraps a StartGenerator and counts GenerateStarts calls for a single watched
// chunk pos — the recompute the seam must SKIP when the cache is seeded from NBT.
type recomputeSpyGen struct {
	inner   structure.StartGenerator
	watch   level.ChunkPos
	recomps atomic.Int64
}

func (s *recomputeSpyGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler structure.SurfaceSampler, biomeAt structure.BiomeAt) []*structure.StructureStart {
	if pos == s.watch {
		s.recomps.Add(1)
	}
	return s.inner.GenerateStarts(seed, pos, sampler, biomeAt)
}

// writeChunkBlobToMca writes a serialized per-chunk blob into a fresh r.<rx>.<rz>.mca at the
// in-region cell for pos, padded to a full sector so the worker's region reader accepts it.
func writeChunkBlobToMca(t *testing.T, dir string, pos level.ChunkPos, blob []byte) {
	t.Helper()
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := region.At(cx, cz)
	ix, iz := region.In(cx, cz)
	name := filepath.Join(dir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")
	r, err := region.Create(name)
	if err != nil {
		t.Fatalf("region.Create: %v", err)
	}
	if err := r.WriteSector(ix, iz, blob); err != nil {
		t.Fatalf("WriteSector: %v", err)
	}
	if err := r.PadToFullSector(); err != nil {
		t.Fatalf("PadToFullSector: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close mca: %v", err)
	}
}

// TestStructureStartsSurviveReloadWithoutRecompute is the SC4 runtime proof. The seed-38 desert
// pyramid owns chunk (-58,32). We:
//  1. decorate that owner via the real pipeline (seeds the SOURCE cache with the computed start),
//  2. SerializeChunkData it (the WRITE path emits the `structures` compound),
//  3. write the blob to a region .mca,
//  4. load it through a FRESH NoiseGenerator wrapped with a recompute spy + a region-backed Worker,
//  5. assert the fresh cache is SEEDED from NBT (StartsCachedFor hit) AND the spy recorded ZERO
//     recomputes for the owner — the start survived the reload WITHOUT recompute.
func TestStructureStartsSurviveReloadWithoutRecompute(t *testing.T) {
	owner := level.ChunkPos{desertPyramidChunkX, desertPyramidChunkZ}

	// (1) SOURCE generator: decorate the owner so its starts are computed + cached.
	src := NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY)
	ch := decorateChunkVia(src, owner)
	srcStarts, ok := src.structCache.StartsCachedFor(owner)
	if !ok || len(srcStarts) == 0 {
		t.Fatalf("source decorate cached no starts for owner %v (expected the desert pyramid)", owner)
	}

	// (2) WRITE path: serialize the chunk WITH its `structures` compound.
	minY, _ := src.Dims()
	blob, err := SerializeChunkData(src.StructureCache(), owner, ch, minY)
	if err != nil {
		t.Fatalf("SerializeChunkData: %v", err)
	}

	// (3) write the blob to a region file the worker will read.
	dir := t.TempDir()
	writeChunkBlobToMca(t, dir, owner, blob)

	// (4) FRESH generator with a recompute spy on the owner pos, wired into a region-backed Worker.
	fresh := NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY)
	spy := &recomputeSpyGen{inner: fresh.structGen, watch: owner}
	fresh.structGen = spy
	if fresh.StructureCache() == nil {
		t.Fatal("fresh NoiseGenerator exposes a nil StructureCache (read-path wiring cannot seed)")
	}

	w := NewWorker(fresh, dir, 8)
	loaded, err := w.loadOrGenerate(owner)
	if err != nil {
		t.Fatalf("worker loadOrGenerate(region hit): %v", err)
	}
	if loaded == nil {
		t.Fatal("worker returned a nil chunk for a present region cell")
	}

	// (5a) the fresh cache was SEEDED from the persisted `structures` NBT (no recompute path taken).
	seeded, ok := fresh.structCache.StartsCachedFor(owner)
	if !ok || len(seeded) == 0 {
		t.Fatalf("fresh cache NOT seeded from persisted structures NBT for owner %v (ok=%v len=%d) — the read-path wiring did not seed", owner, ok, len(seeded))
	}
	if len(seeded) != len(srcStarts) {
		t.Fatalf("seeded start count %d != source %d", len(seeded), len(srcStarts))
	}

	// (5b) the recompute spy must NOT have fired for the owner: the seeded start short-circuited
	// ComputeStarts(owner). This is the "survive WITHOUT recompute" proof.
	if n := spy.recomps.Load(); n != 0 {
		t.Fatalf("owner %v starts were RECOMPUTED %d time(s) after a region load — persisted starts did not short-circuit the recompute", owner, n)
	}

	// And a direct ComputeStarts(owner) on the fresh generator is a cache HIT (still zero recomputes).
	got := fresh.structCache.ComputeStarts(desertPyramidSeed, owner, fresh.structGen)
	if len(got) == 0 {
		t.Fatalf("ComputeStarts(owner) on the seeded cache returned no starts")
	}
	if n := spy.recomps.Load(); n != 0 {
		t.Fatalf("ComputeStarts(owner) recomputed (%d) despite the seeded cache — not a cache hit", n)
	}
}

// TestStructureReloadMissingTagRecomputes is the robustness half: a region chunk with NO
// `structures` tag (an older save, or a structure-free chunk) loads fine and the generator
// RECOMPUTES on demand — the persisted tag is a pure optimization, its absence is a no-op, never
// a panic (T-20-07). We serialize the SAME owner chunk with a nil cache (no structures compound),
// then load it and confirm a subsequent ComputeStarts recomputes (the spy fires) rather than the
// seam wedging or panicking.
func TestStructureReloadMissingTagRecomputes(t *testing.T) {
	owner := level.ChunkPos{desertPyramidChunkX, desertPyramidChunkZ}

	src := NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY)
	ch := decorateChunkVia(src, owner)
	minY, _ := src.Dims()

	// Serialize WITHOUT a structures compound (nil cache -> no `structures` tag).
	blob, err := SerializeChunkData(nil, owner, ch, minY)
	if err != nil {
		t.Fatalf("SerializeChunkData(nil cache): %v", err)
	}

	dir := t.TempDir()
	writeChunkBlobToMca(t, dir, owner, blob)

	fresh := NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY)
	spy := &recomputeSpyGen{inner: fresh.structGen, watch: owner}
	fresh.structGen = spy

	w := NewWorker(fresh, dir, 8)
	if _, err := w.loadOrGenerate(owner); err != nil {
		t.Fatalf("worker loadOrGenerate(no-structures region hit): %v", err)
	}

	// The cache was NOT seeded (no tag) — a fresh ComputeStarts must recompute (spy fires once).
	if _, ok := fresh.structCache.StartsCachedFor(owner); ok {
		t.Fatal("a chunk with no `structures` tag must NOT seed the cache")
	}
	got := fresh.structCache.ComputeStarts(desertPyramidSeed, owner, fresh.structGen)
	if len(got) == 0 {
		t.Fatalf("recompute after a tag-less reload returned no starts for the pyramid owner")
	}
	if n := spy.recomps.Load(); n != 1 {
		t.Fatalf("expected exactly 1 recompute for the owner after a tag-less reload, got %d", n)
	}
}
