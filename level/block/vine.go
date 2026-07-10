package block

// vine.go -- block-family predicates and state accessors for the VINE random-tick spread handler
// (server/vine.go), ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.VineBlock). A small new file keeps the generated blocks.go
// untouched. CITE per function.

// IsVine reports Blocks.VINE. CITE: VineBlock (Blocks.VINE).
func IsVine(s StateID) bool { return isType[Vine](s) }

// VineFace is state.getValue(getPropertyForFace(dir)) for a vine -- the boolean face flag for one of
// the five vine faces (UP, NORTH, SOUTH, WEST, EAST). DOWN has no property (returns false). A
// non-vine state returns false. CITE: VineBlock.getPropertyForFace / PROPERTY_BY_DIRECTION
// (UP/NORTH/SOUTH/WEST/EAST BooleanProperties).
func VineFace(s StateID, dir Direction) bool {
	v, ok := vineOf(s)
	if !ok {
		return false
	}
	switch dir {
	case Up:
		return bool(v.Up)
	case North:
		return bool(v.North)
	case South:
		return bool(v.South)
	case West:
		return bool(v.West)
	case East:
		return bool(v.East)
	default:
		return false // DOWN (no property)
	}
}

// VineHasFaceProperty reports whether dir is one of the vine's five face properties (UP + the four
// horizontals). DOWN has no property. Mirrors getPropertyForFace being defined only for those five.
// CITE: VineBlock.PROPERTY_BY_DIRECTION (no DOWN entry); VineBlock.UP.
func VineHasFaceProperty(dir Direction) bool {
	switch dir {
	case Up, North, South, West, East:
		return true
	default:
		return false
	}
}

// VineWithFace returns the vine state with getPropertyForFace(dir) set to val. ok=false for a
// non-vine s or a dir with no face property (DOWN). CITE: state.setValue(getPropertyForFace(dir),
// val).
func VineWithFace(s StateID, dir Direction, val bool) (StateID, bool) {
	v, ok := vineOf(s)
	if !ok {
		return s, false
	}
	b := Boolean(false)
	if val {
		b = Boolean(true)
	}
	switch dir {
	case Up:
		v.Up = b
	case North:
		v.North = b
	case South:
		v.South = b
	case West:
		v.West = b
	case East:
		v.East = b
	default:
		return s, false // DOWN (no property)
	}
	id, ok := ToStateID[v]
	return id, ok
}

// VineDefaultState is Blocks.VINE.defaultBlockState() -- all faces false. CITE: VineBlock default
// (all BooleanProperties default false).
func VineDefaultState() StateID {
	return ToStateID[Vine{}]
}

// VineHasHorizontalConnection is VineBlock.hasHorizontalConnection(state): true iff any of the four
// horizontal faces (NORTH/EAST/SOUTH/WEST) is set. CITE: VineBlock.hasHorizontalConnection.
func VineHasHorizontalConnection(s StateID) bool {
	v, ok := vineOf(s)
	if !ok {
		return false
	}
	return bool(v.North) || bool(v.East) || bool(v.South) || bool(v.West)
}

// vineOf resolves s to a Vine struct value. CITE: instanceof VineBlock state.
func vineOf(s StateID) (Vine, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Vine{}, false
	}
	v, ok := StateList[s].(Vine)
	return v, ok
}
