package lighting

import "github.com/imhinotori/sulfur/level/block"

// Default block states the engine falls back to, resolved once. CITE: the light engine references
// Blocks.AIR.defaultBlockState() (the isFromEmptyShape "from" state in propagateIncrease) and
// Blocks.BEDROCK.defaultBlockState() (LightEngine.getState when the chunk is not loaded).
var (
	airState     = block.ToStateID[block.Air{}]     // Blocks.AIR.defaultBlockState()
	bedrockState = block.ToStateID[block.Bedrock{}] // Blocks.BEDROCK.defaultBlockState()
)
