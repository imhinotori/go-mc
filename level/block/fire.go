package block

// fire.go — state predicates + property accessors for the FIRE block (net.minecraft.world.level
// .block.FireBlock / BaseFireBlock), ported 1:1 from the unobfuscated 26.2 jar. They back the
// scheduled-tick FireBlock spread/burn-out handler in the server package (server/fire_block.go).
//
// FIRE state (CITE: FireBlock createBlockStateDefinition):
//   AGE   IntegerProperty 0..15 (BlockStateProperties.AGE_15, MAX_AGE == 15)
//   NORTH/EAST/SOUTH/WEST/UP  BooleanProperty (the visual attach flags)
//
// The AGE property is the only value the tick logic mutates numerically; the directional booleans
// are recomputed by getStateForPlacement (canBurn of each neighbor). CITE: FireBlock.AGE / MAX_AGE.

// FireMaxAge is FireBlock.MAX_AGE — the highest AGE value (15). CITE: FireBlock.MAX_AGE == 15
// (BaseFireBlock AGE property BlockStateProperties.AGE_15).
const FireMaxAge = 15

// IsFire reports whether a state id is the fire block (any age / attach flags). CITE: Blocks.FIRE.
func IsFire(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Fire)
	return ok
}

// FireAge returns the AGE property (0..15) of a fire state, or -1 if not fire. CITE:
// FireBlock.AGE (state.getValue(AGE)).
func FireAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if f, ok := StateList[s].(Fire); ok {
		return int(f.Age)
	}
	return -1
}

// FireWithAge returns the fire state id equal to `s` but with AGE set to `age`, preserving the
// directional attach flags — the port of state.setValue(AGE, age) on a fire state. Returns
// ok=false if `s` is not fire or age is out of 0..15. CITE: FireBlock.tick / checkBurnOut
// (state.setValue(AGE, ...)).
func FireWithAge(s StateID, age int) (StateID, bool) {
	if age < 0 || age > FireMaxAge {
		return 0, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	f, ok := StateList[s].(Fire)
	if !ok {
		return 0, false
	}
	f.Age = Integer(age)
	sid, ok := ToStateID[f]
	return sid, ok
}

// FireStateForPlacement is FireBlock.getStateForPlacement(BlockGetter, BlockPos): the default fire
// state (AGE 0) with each directional attach flag set from canBurn of the neighbor in that
// direction. The caller supplies the neighbor states already read from the world (a neighbor that
// is not present / unreadable should be passed as air, since canBurn(air) == false). `igniteOdds`
// is the caller's flammability lookup (canBurn(s) == igniteOdds(s) > 0). The AGE stays 0 (default).
//
// CITE: FireBlock.getStateForPlacement(BlockGetter, BlockPos): else-branch after the below-support
// early return — for each Direction d set PROPERTY_BY_DIRECTION[d] = canBurn(getBlockState(
// pos.relative(d))). Only the horizontal + UP directions have a PROPERTY (DOWN has none), so the
// `down` neighbor is not needed here (the below-support early return is handled by the caller).
func FireStateForPlacement(up, north, south, west, east StateID, igniteOdds func(StateID) int) (StateID, bool) {
	burn := func(s StateID) bool { return igniteOdds(s) > 0 }
	f := Fire{
		Age:   0,
		North: Boolean(burn(north)),
		East:  Boolean(burn(east)),
		South: Boolean(burn(south)),
		West:  Boolean(burn(west)),
		Up:    Boolean(burn(up)),
	}
	sid, ok := ToStateID[f]
	return sid, ok
}

// FireDefaultState returns the default fire state (AGE 0, all attach flags false) — the
// defaultBlockState() early-return branch of getStateForPlacement. CITE:
// FireBlock.getStateForPlacement (return defaultBlockState()).
func FireDefaultState() (StateID, bool) {
	sid, ok := ToStateID[Fire{}]
	return sid, ok
}

// IsInfiniburnOverworld is BlockState.is(#infiniburn_overworld) — the block set that makes fire on
// top of it NEVER burn out (the "infiniburn" check FireBlock.tick reads via
// dimensionType().infiniburn()). Tag closure (26.2 datagen infiniburn_overworld.json): {
// netherrack, magma_block }. This is the OVERWORLD infiniburn; nether/end have their own sets but
// v1 fire spread runs in the overworld dimension. CITE: DimensionType.infiniburn() /
// #infiniburn_overworld.
func IsInfiniburnOverworld(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Netherrack, MagmaBlock:
		return true
	default:
		return false
	}
}

// IsTntBlock reports whether a state id is TNT — the checkBurnOut `if (block instanceof TntBlock)
// TntBlock.prime(...)` branch. CITE: FireBlock.checkBurnOut (instanceof TntBlock).
func IsTntBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Tnt)
	return ok
}
