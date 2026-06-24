package carver

import "github.com/imhinotori/sulfur/level"

// caveWorldCarver ports net.minecraft.world.level.levelgen.carver.CaveWorldCarver.
// Task 2 fills the full tunnel walk; this file holds the isStartChunk probability
// roll the Task-1 driver needs.
type caveWorldCarver struct{}

// isStartChunk ports CaveWorldCarver.isStartChunk = rng.nextFloat() <= probability.
func (caveWorldCarver) isStartChunk(cfg *CarverConfig, rng *legacyRandom) bool {
	return rng.nextFloat() <= float32(cfg.Probability)
}

// carve is ported in Task 2 (createRoom + createTunnel).
func (caveWorldCarver) carve(cfg *CarverConfig, cc *carveContext, rng *legacyRandom, src level.ChunkPos) bool {
	return false
}
