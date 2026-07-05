package lighting

import "github.com/imhinotori/sulfur/level/block"

// LightChunk ports net.minecraft.world.level.chunk.LightChunk (the read view the engine has of one
// chunk column). CITE: LightChunk / LightChunkGetter.getChunkForLighting.
type LightChunk interface {
	// GetBlockState returns the block state at the world (x,y,z). CITE: BlockGetter.getBlockState
	// (used via LightEngine.getState -> chunk.getBlockState(pos)).
	GetBlockState(x, y, z int) block.StateID
	// SkyLightSources returns this chunk's per-column lowest-sky-source-Y heightmap. CITE:
	// LightChunk.getSkyLightSources.
	SkyLightSources() *ChunkSkyLightSources
	// FindBlockLightSources invokes fn for every emissive block position in the chunk (used by
	// BlockLightEngine.propagateLightSources). CITE: LightChunk.findBlockLightSources.
	FindBlockLightSources(fn func(x, y, z int, state block.StateID))
}

// LightChunkGetter ports net.minecraft.world.level.chunk.LightChunkGetter. CITE.
type LightChunkGetter interface {
	// GetChunkForLighting returns the chunk at (chunkX, chunkZ), or nil if not loaded. CITE:
	// LightChunkGetter.getChunkForLighting.
	GetChunkForLighting(chunkX, chunkZ int) LightChunk
	// MinSectionY / SectionsCount describe the level's vertical geometry (LevelHeightAccessor).
	// The sky engine's empty-source heightmap uses (minSectionY, sectionsCount) to compute the
	// world-top used for fully-lit columns. CITE: chunkSource.getLevel() (LevelHeightAccessor).
	MinSectionY() int
	SectionsCount() int
}
