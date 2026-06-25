package structure

import (
	"strconv"

	"github.com/puzpuzpuz/xsync/v4"
	"golang.org/x/sync/singleflight"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// referenceRadius is the chunk radius the REFERENCES scan covers (jar-confirmed ±8 in
// ChunkGenerator.createReferences). 8 is the maximum structure radius the engine
// assumes — a structure owned by a chunk up to 8 away can still write into the center.
// The SCAN radius and the start-COMPUTE radius are THE SAME 8: a radius-8 scan reading
// only radius-1 cached starts would silently truncate any start owned >=2 chunks out
// (latent for <=2-chunk temples, a LIVE bug the moment Phase-15 mineshafts reuse this).
const referenceRadius = 8

// BiomeAt is the real biome-check seam (BLOCKER fix, no accept-by-default): the
// StartGenerator gates each temple's start on GetBiome-at-origin in its has_structure
// allow-set. NoiseGenerator threads its MultiNoiseBiomeSource.GetBiome here. 14-02/14-03
// fire the gate; STRUCT-01 supplies a no-op generator so the pipeline runs with zero
// starts (and the byte output is unchanged).
type BiomeAt func(wx, wy, wz int) levelbiome.Type

// StartGenerator is the per-world structure-start producer the cache memoizes. 14-02's
// structures implement it (the desert-pyramid set, then jungle/igloo/swamp_hut). It is
// PURE over (seed, pos): same inputs -> same starts -> identical cache. It is given BOTH
// a SurfaceSampler (the heightmap-at-STARTS column query) AND a BiomeAt (the real
// biome gate) so the temple decision uses real terrain + real biome — NO accept-by-default.
type StartGenerator interface {
	// GenerateStarts produces the structure starts OWNED by pos (pure over (seed,pos)).
	// An empty result means "no structure owned here". 14-02 fills the piece trees;
	// the STRUCT-01 no-op generator returns nil for every pos.
	GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart
}

// Cache is the concurrent StructureStart cache: a pure, singleflight-deduped
// memoization of the STARTS decision plus the per-chunk REFERENCES list.
//
//   - Starts[packedPos] = the starts OWNED by that chunk (computed pure over (seed,pos)).
//   - References[packedPos] = the packed keys of NEIGHBOR chunks whose owned start bbox
//     reaches into this chunk's 16x16 column (the cross-chunk discovery list).
//
// Both maps are xsync.Map (the ONLY cross-goroutine state; reads obstruction-free,
// writes lock-sharded). Because STARTS is pure over (seed,pos), the cache is a
// MEMOIZATION, not dangerous shared mutable state — two workers computing the same
// chunk's starts via singleflight get byte-identical results.
type Cache struct {
	starts     *xsync.Map[int64, []*StructureStart]
	references *xsync.Map[int64, []int64]
	sampler    SurfaceSampler
	biomeAt    BiomeAt
	sf         singleflight.Group
}

// NewCache builds the cache over a surface sampler + biome lookup (both threaded from
// the NoiseGenerator). The sampler/biomeAt are the SAME for every (seed,pos) so the
// memoization stays pure.
func NewCache(sampler SurfaceSampler, biomeAt BiomeAt) *Cache {
	return &Cache{
		starts:     xsync.NewMap[int64, []*StructureStart](),
		references: xsync.NewMap[int64, []int64](),
		sampler:    sampler,
		biomeAt:    biomeAt,
	}
}

// packPos packs (cx,cz) into the int64 cache key — the SAME packing world.packPos uses
// (int64(p[0])<<32 | int64(uint32(p[1]))), so the worker and the cache agree on keys.
func packPos(p level.ChunkPos) int64 {
	return int64(p[0])<<32 | int64(uint32(p[1]))
}

// ComputeStarts returns the starts OWNED by pos, memoized + singleflight-deduped over
// the packed key (exactly like chunk-gen dedup): two concurrent computers of the same
// (seed,pos) collapse to ONE generator call and both receive the identical slice. PURE
// over (seed,pos) — the cache is a memoization. A cache hit returns immediately.
func (c *Cache) ComputeStarts(seed int64, pos level.ChunkPos, gen StartGenerator) []*StructureStart {
	key := packPos(pos)
	if v, ok := c.starts.Load(key); ok {
		return v
	}
	sfKey := strconv.FormatInt(key, 10)
	v, _, _ := c.sf.Do(sfKey, func() (any, error) {
		// Re-check under the singleflight (another caller may have stored while we waited).
		if existing, ok := c.starts.Load(key); ok {
			return existing, nil
		}
		starts := gen.GenerateStarts(seed, pos, c.sampler, c.biomeAt)
		c.starts.Store(key, starts)
		return starts, nil
	})
	return v.([]*StructureStart)
}

// ComputeReferences ports ChunkGenerator.createReferences as the 8-chunk-radius
// bbox-intersect scan for chunk C: scan every cell in [C.x-8..C.x+8]x[C.z-8..C.z+8],
// COMPUTE-STARTS-ON-DEMAND for each cell (ComputeStarts — pure + singleflight-deduped,
// so it is cheap, order-independent, and memoized), and for every VALID owned start
// whose bbox XZ-intersects C's 16x16 column, record the owner's packed key in C's
// References (deduped). A start owned by a chunk up to 8 away is FOUND from C this way.
//
// The scan radius and the compute radius are THE SAME 8 — REFERENCES drives its own
// ComputeStarts per scanned cell rather than reading only the already-cached ring, so a
// start owned >=2 chunks out is never silently truncated (future-proofing Phase-15
// multi-chunk mineshafts). The result is memoized in References[C].
func (c *Cache) ComputeReferences(seed int64, center level.ChunkPos, gen StartGenerator, minY, height int) []int64 {
	key := packPos(center)
	if v, ok := c.references.Load(key); ok {
		return v
	}

	col := WritableArea(center, minY, height)
	seen := make(map[int64]bool)
	var refs []int64

	for dx := -referenceRadius; dx <= referenceRadius; dx++ {
		for dz := -referenceRadius; dz <= referenceRadius; dz++ {
			cell := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
			// COMPUTE-ON-DEMAND: pure + singleflight-deduped, so the radius-8 scan reads
			// radius-8 data (not a radius-1 pre-cached ring).
			for _, st := range c.ComputeStarts(seed, cell, gen) {
				if !st.IsValid() {
					continue
				}
				if !st.BBox.IntersectsXZ(col.MinX, col.MinZ, col.MaxX, col.MaxZ) {
					continue
				}
				ownerKey := packPos(st.ChunkPos)
				if seen[ownerKey] {
					continue
				}
				seen[ownerKey] = true
				refs = append(refs, ownerKey)
			}
		}
	}

	c.references.Store(key, refs)
	return refs
}

// StartsForChunk gathers the starts the PLACE pass (14-02) writes into C: C's OWN
// starts plus the starts named in C's References (neighbor starts reaching into C). It
// reads the cache populated by ComputeStarts + ComputeReferences (which the worker runs
// during Decorate). Returns only VALID starts.
func (c *Cache) StartsForChunk(center level.ChunkPos) []*StructureStart {
	var out []*StructureStart

	if own, ok := c.starts.Load(packPos(center)); ok {
		for _, st := range own {
			if st.IsValid() {
				out = append(out, st)
			}
		}
	}

	if refs, ok := c.references.Load(packPos(center)); ok {
		for _, ownerKey := range refs {
			if owned, ok := c.starts.Load(ownerKey); ok {
				for _, st := range owned {
					if st.IsValid() {
						out = append(out, st)
					}
				}
			}
		}
	}

	return out
}

// noopStartGenerator is the STRUCT-01 inert generator: it owns NO structures, so every
// chunk's starts are empty and the pipeline runs end-to-end with ZERO blocks placed
// (proving the worker seam is byte-stable before any geometry). 14-02 replaces it with
// the desert-pyramid set.
type noopStartGenerator struct{}

// NoopStartGenerator returns the inert generator (zero starts for every chunk).
func NoopStartGenerator() StartGenerator { return noopStartGenerator{} }

func (noopStartGenerator) GenerateStarts(int64, level.ChunkPos, SurfaceSampler, BiomeAt) []*StructureStart {
	return nil
}
