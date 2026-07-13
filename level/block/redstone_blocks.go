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

// NoteBlockCycleNote resolves the note_block state with NOTE cycled to (note+1)%25. The NOTE property
// is the 0..24 IntegerProperty; BlockState.cycle(NOTE) advances to the next value in that range, wrapping
// past the max (24) back to the min (0). Returns (s, false) for a non-note-block. CITE:
// NoteBlock.useWithoutItem (state.cycle(NOTE)).
func NoteBlockCycleNote(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		b.Note = Integer((int(b.Note) + 1) % 25)
		return lookup(b)
	}
	return s, false
}

// NoteBlockWithInstrument resolves the note_block state with INSTRUMENT=inst, preserving NOTE/POWERED.
// Returns (s, false) for a non-note-block. CITE: NoteBlock.setInstrument (state.setValue(INSTRUMENT, ...)).
func NoteBlockWithInstrument(s StateID, inst NoteBlockInstrument) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(NoteBlock); ok {
		b.Instrument = inst
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

// ---------------------------------------------------------------------------------------------
// Pressure plates (BasePressurePlateBlock subclasses). 1:1 with the 26.2 jar. See
// server/pressure_plate.go for the reactions. Two families:
//   - PressurePlateBlock (stone/wooden/polished_blackstone): a POWERED boolean -> getSignalForState
//     0/15. Stone + polished_blackstone use MOBS sensitivity (LivingEntity); the wooden variants use
//     EVERYTHING (Entity, includes items).
//   - WeightedPressurePlateBlock (light/heavy): a POWER integer 0..15 -> getSignalForState == POWER;
//     EVERYTHING sensitivity, maxWeight 15 (light) / 150 (heavy).
// CITE: PressurePlateBlock.getSignalForState/setSignalForState; WeightedPressurePlateBlock.*.
// ---------------------------------------------------------------------------------------------

// IsPressurePlate reports whether a state id is any boolean-POWERED pressure plate (stone / wooden /
// polished_blackstone). CITE: PressurePlateBlock.
func IsPressurePlate(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case StonePressurePlate, PolishedBlackstonePressurePlate,
		OakPressurePlate, SprucePressurePlate, BirchPressurePlate, JunglePressurePlate,
		AcaciaPressurePlate, CherryPressurePlate, DarkOakPressurePlate, PaleOakPressurePlate,
		MangrovePressurePlate, BambooPressurePlate, CrimsonPressurePlate, WarpedPressurePlate:
		return true
	default:
		return false
	}
}

// PressurePlateMobsOnly reports whether a plate uses MOBS sensitivity (LivingEntity-only trigger):
// stone + polished_blackstone. All wooden variants use EVERYTHING (any Entity, includes items).
// CITE: BlockSetType.STONE/POLISHED_BLACKSTONE pressurePlateSensitivity == MOBS; OAK/... == EVERYTHING.
func PressurePlateMobsOnly(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case StonePressurePlate, PolishedBlackstonePressurePlate:
		return true
	default:
		return false
	}
}

// PressurePlatePowered returns the POWERED property of a boolean pressure plate, or false if not one.
// CITE: PressurePlateBlock.POWERED.
func PressurePlatePowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case StonePressurePlate:
		return bool(b.Powered)
	case PolishedBlackstonePressurePlate:
		return bool(b.Powered)
	case OakPressurePlate:
		return bool(b.Powered)
	case SprucePressurePlate:
		return bool(b.Powered)
	case BirchPressurePlate:
		return bool(b.Powered)
	case JunglePressurePlate:
		return bool(b.Powered)
	case AcaciaPressurePlate:
		return bool(b.Powered)
	case CherryPressurePlate:
		return bool(b.Powered)
	case DarkOakPressurePlate:
		return bool(b.Powered)
	case PaleOakPressurePlate:
		return bool(b.Powered)
	case MangrovePressurePlate:
		return bool(b.Powered)
	case BambooPressurePlate:
		return bool(b.Powered)
	case CrimsonPressurePlate:
		return bool(b.Powered)
	case WarpedPressurePlate:
		return bool(b.Powered)
	default:
		return false
	}
}

// PressurePlateWithPowered resolves the plate state with POWERED set to `powered`. Returns (s, false)
// if not a boolean plate. CITE: PressurePlateBlock.setSignalForState (setValue(POWERED, i > 0)).
func PressurePlateWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	p := Boolean(powered)
	switch b := StateList[s].(type) {
	case StonePressurePlate:
		b.Powered = p
		return lookup(b)
	case PolishedBlackstonePressurePlate:
		b.Powered = p
		return lookup(b)
	case OakPressurePlate:
		b.Powered = p
		return lookup(b)
	case SprucePressurePlate:
		b.Powered = p
		return lookup(b)
	case BirchPressurePlate:
		b.Powered = p
		return lookup(b)
	case JunglePressurePlate:
		b.Powered = p
		return lookup(b)
	case AcaciaPressurePlate:
		b.Powered = p
		return lookup(b)
	case CherryPressurePlate:
		b.Powered = p
		return lookup(b)
	case DarkOakPressurePlate:
		b.Powered = p
		return lookup(b)
	case PaleOakPressurePlate:
		b.Powered = p
		return lookup(b)
	case MangrovePressurePlate:
		b.Powered = p
		return lookup(b)
	case BambooPressurePlate:
		b.Powered = p
		return lookup(b)
	case CrimsonPressurePlate:
		b.Powered = p
		return lookup(b)
	case WarpedPressurePlate:
		b.Powered = p
		return lookup(b)
	default:
		return s, false
	}
}

// IsWeightedPressurePlate reports whether a state id is a weighted pressure plate (light/heavy). CITE:
// WeightedPressurePlateBlock.
func IsWeightedPressurePlate(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case LightWeightedPressurePlate, HeavyWeightedPressurePlate:
		return true
	default:
		return false
	}
}

// WeightedPressurePlateMaxWeight is the maxWeight ctor arg: 15 for light, 150 for heavy. CITE:
// Blocks.LIGHT_WEIGHTED_PRESSURE_PLATE(15) / HEAVY_WEIGHTED_PRESSURE_PLATE(150).
func WeightedPressurePlateMaxWeight(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0
	}
	switch StateList[s].(type) {
	case LightWeightedPressurePlate:
		return 15
	case HeavyWeightedPressurePlate:
		return 150
	default:
		return 0
	}
}

// WeightedPressurePlatePower returns the POWER property of a weighted plate, or 0 if not one. CITE:
// WeightedPressurePlateBlock.getSignalForState (getValue(POWER)).
func WeightedPressurePlatePower(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0
	}
	switch b := StateList[s].(type) {
	case LightWeightedPressurePlate:
		return int(b.Power)
	case HeavyWeightedPressurePlate:
		return int(b.Power)
	default:
		return 0
	}
}

// WeightedPressurePlateWithPower resolves the weighted plate state with POWER set to `power` (0..15).
// Returns (s, false) if not one or the power is out of range. CITE: WeightedPressurePlateBlock
// .setSignalForState (setValue(POWER, i)).
func WeightedPressurePlateWithPower(s StateID, power int) (StateID, bool) {
	if power < 0 || power > 15 {
		return s, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	switch b := StateList[s].(type) {
	case LightWeightedPressurePlate:
		b.Power = Integer(power)
		return lookup(b)
	case HeavyWeightedPressurePlate:
		b.Power = Integer(power)
		return lookup(b)
	default:
		return s, false
	}
}
