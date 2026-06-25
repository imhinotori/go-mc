package structure

import "github.com/imhinotori/sulfur/level"

// CompositeStartGenerator dispatches a chunk's start decision to a fixed set of per-structure
// StartGenerators (the four scattered temples: desert pyramid + jungle temple + igloo + swamp
// hut) and concatenates their owned starts. Each member is PURE over (seed,pos), so the
// composite is too — the cache memoizes it identically.
//
// A chunk can own at most one structure per structure_set (the random_spread math), but two
// DIFFERENT sets may both pick the same chunk; concatenating the per-generator results is the
// jar-faithful "all structure_sets generate independently" behavior. Each member runs its own
// biome gate, so a chunk owned by jungle's set but sitting in a desert biome produces no
// jungle start (and vice-versa) — the per-temple biome checks stay load-bearing.
type CompositeStartGenerator struct {
	gens []StartGenerator
}

// NewCompositeStartGenerator builds the composite over the given members (in order). The
// dispatch order only affects the slice order of returned starts, not which chunk owns what
// (each member's ownership is independent), so the placement stays pure + order-independent
// per (seed,pos).
func NewCompositeStartGenerator(gens ...StartGenerator) *CompositeStartGenerator {
	return &CompositeStartGenerator{gens: gens}
}

// GenerateStarts runs every member generator for pos and concatenates the valid starts.
func (c *CompositeStartGenerator) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	var out []*StructureStart
	for _, g := range c.gens {
		out = append(out, g.GenerateStarts(seed, pos, sampler, biomeAt)...)
	}
	return out
}
