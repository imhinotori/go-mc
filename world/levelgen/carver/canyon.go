package carver

import "github.com/imhinotori/sulfur/level"

// canyonWorldCarver ports net.minecraft.world.level.levelgen.carver.CanyonWorldCarver
// (RAVINES). Task 2 fills the full canyon walk; this file holds the isStartChunk
// probability roll the Task-1 driver needs.
type canyonWorldCarver struct{}

// isStartChunk ports CanyonWorldCarver.isStartChunk = rng.nextFloat() <= probability.
func (canyonWorldCarver) isStartChunk(cfg *CarverConfig, rng *legacyRandom) bool {
	return rng.nextFloat() <= float32(cfg.Probability)
}

// carve is ported in Task 2 (doCarve + initWidthFactors + updateVerticalRadius).
func (canyonWorldCarver) carve(cfg *CarverConfig, cc *carveContext, rng *legacyRandom, src level.ChunkPos) bool {
	return false
}
