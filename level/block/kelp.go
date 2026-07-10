package block

// kelp.go -- block-family predicates and state accessors for the KELP random-tick handler
// (server/kelp.go), ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.KelpBlock / GrowingPlantHeadBlock). A small new file keeps the
// generated blocks.go untouched. CITE per function.

// KelpMaxAge is GrowingPlantHeadBlock.MAX_AGE (25) -- the shared head max age. CITE:
// GrowingPlantHeadBlock.MAX_AGE (25).
const KelpMaxAge = 25

// KelpGrowChance is KelpBlock's growPerTickProbability (0.14d) passed to the GrowingPlantHeadBlock
// ctor. CITE: KelpBlock ctor (ldc2_w 0.14d); GrowingPlantHeadBlock.growPerTickProbability.
const KelpGrowChance = 0.14

// IsKelp reports Blocks.KELP (the growing kelp HEAD, any AGE). KELP_PLANT (the body) is a separate
// block that does NOT random-tick, so it is excluded. CITE: KelpBlock (Blocks.KELP);
// KelpBlock.getBodyBlock (Blocks.KELP_PLANT).
func IsKelp(s StateID) bool { return isType[Kelp](s) }

// KelpAge is state.getValue(GrowingPlantHeadBlock.AGE) (0..25) for a kelp head, or -1 otherwise.
// CITE: GrowingPlantHeadBlock.AGE (IntegerProperty 0..25).
func KelpAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if k, ok := StateList[s].(Kelp); ok {
		return int(k.Age)
	}
	return -1
}

// KelpWithAge returns the kelp head state with AGE set to age (0..25). ok=false for a non-kelp s or
// an out-of-range age. This is GrowingPlantHeadBlock.getGrowIntoState(state, random) for kelp ==
// state.cycle(AGE) (increment AGE; the AGE<25 gate prevents a wrap). CITE:
// GrowingPlantHeadBlock.getGrowIntoState (state.cycle(AGE)).
func KelpWithAge(s StateID, age int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > KelpMaxAge {
		return s, false
	}
	if _, ok := StateList[s].(Kelp); !ok {
		return s, false
	}
	id, ok := ToStateID[Kelp{Age: Integer(age)}]
	return id, ok
}

// IsWaterBlock reports BlockState.is(Blocks.WATER) -- the strict WATER block identity (any LEVEL,
// source or flowing), NOT a waterlogged block (whose block is stairs/kelp/etc.). This is
// KelpBlock.canGrowInto's exact check `state.is(Blocks.WATER)` -- kelp grows up into a water cell
// only, and a waterlogged cell is NOT the water block. CITE: KelpBlock.canGrowInto
// (state.is(Blocks.WATER)).
func IsWaterBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Water)
	return ok
}
