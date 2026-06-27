package block

func IsAir(s StateID) bool {
	return IsAirBlock(StateList[s])
}

func IsAirBlock(b Block) bool {
	switch b.(type) {
	case Air, CaveAir, VoidAir:
		return true
	default:
		return false
	}
}

// IsFluid reports whether a state id is a fluid (water or lava) — i.e. its vanilla
// getFluidState() is non-empty. This is the predicate behind the chunk section's
// fluidCount short (LevelChunkSection.nonEmptyFluidCount): the client uses that count to
// decide whether a section has fluid to TICK/simulate. A water/lava block at ANY level
// (source or flowing) counts. Used by the section encoder so generated water is reported
// as fluid to the client (without it the client treats the section as fluid-free and a
// player will not float in generated/aquifer water until a block update wakes the cell).
func IsFluid(s StateID) bool {
	return IsFluidBlock(StateList[s])
}

// IsFluidBlock is the Block-typed form of IsFluid (water or lava, any level).
func IsFluidBlock(b Block) bool {
	switch b.(type) {
	case Water, Lava:
		return true
	default:
		return false
	}
}

// IsVegetationGround reports whether a state id is a block a vegetation feature (sapling/tree,
// grass, flowers) may sit ON — the vanilla `#minecraft:substrate_overworld` tag (the ground
// half of `SUPPORTS_VEGETATION`) plus farmland. substrate_overworld nests
// #dirt {dirt, coarse_dirt, rooted_dirt} + #grass_blocks {grass_block, podzol, mycelium} +
// #moss_blocks {moss_block, pale_moss_block} + #mud {mud, muddy_mangrove_roots}. Crucially this
// EXCLUDES logs and leaves, so the worldgen tree `would_survive` filter using this correctly
// rejects a tree origin that landed on another tree's log/leaf column — the fix for trees
// generating on top of trees. CITE: BlockTags.substrate_overworld (extracted tag JSON) +
// VegetationBlock.mayPlaceOn (state.is(#dirt) style ground check) + Blocks.FARMLAND.
func IsVegetationGround(s StateID) bool {
	switch StateList[s].(type) {
	case Dirt, CoarseDirt, RootedDirt, // #dirt
		GrassBlock, Podzol, Mycelium, // #grass_blocks
		MossBlock, PaleMossBlock, // #moss_blocks
		Mud, MuddyMangroveRoots, // #mud
		Farmland: // SUPPORTS_VEGETATION adds farmland
		return true
	default:
		return false
	}
}
