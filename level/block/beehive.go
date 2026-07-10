package block

// beehive.go -- BEEHIVE + BEE_NEST state predicates + property accessors, ported 1:1 from the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap this session). They back the BeehiveBlockEntity
// hive/pollination loop in the server package (beehive_be.go), mirroring the shape of growth.go
// (SaplingStage/SaplingCycleStage) for a two-block family (minecraft:beehive + minecraft:bee_nest, both
// BeehiveBlock instances with the same FACING + HONEY_LEVEL 0..5 properties).
//
// CITE:
//   BeehiveBlock.HONEY_LEVEL == BlockStateProperties.LEVEL_HONEY (IntegerProperty 0..5).
//   BeehiveBlock.FACING == HorizontalDirectionalBlock.FACING (a horizontal Direction).
//   BeehiveBlockEntity.getHoneyLevel(state) == state.getValue(BeehiveBlock.HONEY_LEVEL).
//   BlockTags.BEEHIVES == {minecraft:beehive, minecraft:bee_nest} (the two BeehiveBlock members).

// IsBeehiveBlock reports whether a state id is a BeehiveBlock -- minecraft:beehive OR minecraft:bee_nest
// (the two members of BlockTags.BEEHIVES, both BeehiveBlock instances). CITE: BeehiveBlock / BlockTags.BEEHIVES.
func IsBeehiveBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Beehive, BeeNest:
		return true
	default:
		return false
	}
}

// HoneyLevel returns the HONEY_LEVEL property (0..5) of a beehive/bee_nest state, or -1 when the state is
// not a BeehiveBlock. CITE: BeehiveBlockEntity.getHoneyLevel (state.getValue(BeehiveBlock.HONEY_LEVEL)).
func HoneyLevel(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case Beehive:
		return int(b.HoneyLevel)
	case BeeNest:
		return int(b.HoneyLevel)
	default:
		return -1
	}
}

// BeehiveFacing returns the FACING Direction of a beehive/bee_nest state (the horizontal facing the hive
// entrance points, along which released bees exit). ok=false when the state is not a BeehiveBlock. CITE:
// releaseOccupant (state.getValue(BeehiveBlock.FACING) -> pos.relative(direction)).
func BeehiveFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	switch b := StateList[s].(type) {
	case Beehive:
		return b.Facing, true
	case BeeNest:
		return b.Facing, true
	default:
		return Down, false
	}
}

// WithHoneyLevel returns the beehive/bee_nest state id with HONEY_LEVEL set to hl (0..5), preserving the
// FACING. ok=false when the state is not a BeehiveBlock or hl is out of range. CITE: releaseOccupant
// (state.setValue(BeehiveBlock.HONEY_LEVEL, hl + i)).
func WithHoneyLevel(s StateID, hl int) (StateID, bool) {
	if hl < 0 || hl > 5 || int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	switch b := StateList[s].(type) {
	case Beehive:
		sid, ok := ToStateID[Beehive{Facing: b.Facing, HoneyLevel: Integer(hl)}]
		return sid, ok
	case BeeNest:
		sid, ok := ToStateID[BeeNest{Facing: b.Facing, HoneyLevel: Integer(hl)}]
		return sid, ok
	default:
		return 0, false
	}
}
