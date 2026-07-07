package block

// redstone_blocks.go - block-package accessors for NoteBlock / TargetBlock / DaylightDetector /
// TripwireHook / Tripwire. 1:1 with the 26.2 jar. See server/redstone_blocks.go for the reactions.

func IsNoteBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(NoteBlock)
	return ok
}

func NoteBlockPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		return bool(b.Powered)
	}
	return false
}

func NoteBlockInstrumentOf(s StateID) (NoteBlockInstrument, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		return b.Instrument, true
	}
	return 0, false
}

func NoteBlockNoteOf(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		return int(b.Note)
	}
	return -1
}

func NoteBlockWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}

func IsTargetBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Target)
	return ok
}

func TargetOutputPower(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if b, ok := StateList[s].(Target); ok {
		return int(b.Power)
	}
	return -1
}

func TargetWithOutputPower(s StateID, power int) (StateID, bool) {
	if power < 0 || power > 15 {
		return s, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Target); ok {
		b.Power = Integer(power)
		return lookup(b)
	}
	return s, false
}

func IsDaylightDetector(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(DaylightDetector)
	return ok
}

func DaylightPower(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if b, ok := StateList[s].(DaylightDetector); ok {
		return int(b.Power)
	}
	return -1
}

func DaylightInverted(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(DaylightDetector); ok {
		return bool(b.Inverted)
	}
	return false
}

func DaylightWithPower(s StateID, power int) (StateID, bool) {
	if power < 0 || power > 15 {
		return s, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(DaylightDetector); ok {
		b.Power = Integer(power)
		return lookup(b)
	}
	return s, false
}

func IsTripwireHook(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(TripwireHook)
	return ok
}

func TripwireHookFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	if b, ok := StateList[s].(TripwireHook); ok {
		return b.Facing, true
	}
	return 0, false
}

func TripwireHookPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(TripwireHook); ok {
		return bool(b.Powered)
	}
	return false
}

func TripwireHookAttached(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(TripwireHook); ok {
		return bool(b.Attached)
	}
	return false
}

func TripwireHookWith(s StateID, attached, powered bool, facing Direction) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(TripwireHook); ok {
		b.Attached = Boolean(attached)
		b.Powered = Boolean(powered)
		b.Facing = facing
		return lookup(b)
	}
	return s, false
}

func IsTripwire(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Tripwire)
	return ok
}

func TripwirePowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Tripwire); ok {
		return bool(b.Powered)
	}
	return false
}

func TripwireAttached(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Tripwire); ok {
		return bool(b.Attached)
	}
	return false
}

func TripwireDisarmed(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Tripwire); ok {
		return bool(b.Disarmed)
	}
	return false
}

func TripwireWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Tripwire); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}

func TripwireWithAttached(s StateID, attached bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Tripwire); ok {
		b.Attached = Boolean(attached)
		return lookup(b)
	}
	return s, false
}

// NoteInstrumentWorksAboveNoteBlock is NoteBlockInstrument.worksAboveNoteBlock() == (type != BASE_BLOCK).
// The 20 melodic instruments (HARP..TRUMPET_WEATHERED) are BASE_BLOCK (need clear air above to play); the
// mob-head instruments (ZOMBIE..PIGLIN) are MOB_HEAD and CUSTOM_HEAD is CUSTOM, which work above a note
// block. So the predicate is instrument >= ZOMBIE (the first non-BASE_BLOCK enum value). CITE:
// NoteBlockInstrument.worksAboveNoteBlock (type != BASE_BLOCK) + the per-instrument Type static init.
func NoteInstrumentWorksAboveNoteBlock(i NoteBlockInstrument) bool {
	return i >= NoteBlockInstrumentZombie
}
