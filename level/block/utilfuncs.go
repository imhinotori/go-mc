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
