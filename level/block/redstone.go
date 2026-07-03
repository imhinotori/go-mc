package block

// redstone.go — the block-package half of the CORE REDSTONE port (wire + torch + block + lever +
// button): the per-state predicates and state accessors/resolvers the server-side signal graph
// (server/redstone.go) reads and writes. Every value and branch is a literal 1:1 copy of the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via `javap -c -p` / CFR this
// session. No gameplay is invented here; this file only exposes the vanilla state shape (POWER
// 0..15, LIT, POWERED, FACE/FACING, the four RedstoneSide connection properties) as typed Go
// lookups so the server can run the faithful signal logic.
//
// CITE (classes):
//   net.minecraft.world.level.block.RedStoneWireBlock  (POWER/NORTH/EAST/SOUTH/WEST, shouldConnectTo)
//   net.minecraft.world.level.block.RedstoneTorchBlock / RedstoneWallTorchBlock (LIT, getSignal)
//   net.minecraft.world.level.block.PoweredBlock       (redstone_block: isSignalSource, ownSignal=15)
//   net.minecraft.world.level.block.LeverBlock         (POWERED, FACE/FACING, getConnectedDirection)
//   net.minecraft.world.level.block.ButtonBlock        (POWERED, FACE/FACING)
//   net.minecraft.world.level.block.FaceAttachedHorizontalDirectionalBlock.getConnectedDirection
//   net.minecraft.world.level.block.state.properties.RedstoneSide.isConnected

// IsConnected is RedstoneSide.isConnected(): a side is connected iff it is not NONE (both UP and
// SIDE are connected). CITE: RedstoneSide.isConnected (`this != NONE`).
func (r RedstoneSide) IsConnected() bool { return r != RedstoneSideNone }

// ---------------------------------------------------------------------------------------------
// Redstone wire (RedStoneWireBlock)
// ---------------------------------------------------------------------------------------------

// IsRedstoneWire reports whether a state id is redstone wire (any POWER / connection combination).
// CITE: RedStoneWireBlock.
func IsRedstoneWire(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(RedstoneWire)
	return ok
}

// RedstoneWirePower returns the POWER (0..15) of a wire state, or -1 if not wire. This is
// state.getValue(RedStoneWireBlock.POWER). CITE: RedStoneWireBlock.ownSignal / evaluator.getWireSignal.
func RedstoneWirePower(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if w, ok := StateList[s].(RedstoneWire); ok {
		return int(w.Power)
	}
	return -1
}

// RedstoneWireConnections returns the four RedstoneSide connection properties of a wire state
// (north, east, south, west), matching the wire struct's stored connection state. Returns NONE for
// all four if the state is not wire (defensive). CITE: RedStoneWireBlock NORTH/EAST/SOUTH/WEST.
func RedstoneWireConnections(s StateID) (north, east, south, west RedstoneSide) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return RedstoneSideNone, RedstoneSideNone, RedstoneSideNone, RedstoneSideNone
	}
	if w, ok := StateList[s].(RedstoneWire); ok {
		return w.North, w.East, w.South, w.West
	}
	return RedstoneSideNone, RedstoneSideNone, RedstoneSideNone, RedstoneSideNone
}

// RedstoneWireWithPower resolves the wire state id equal to `s` but with POWER set to `power`
// (0..15), preserving the four connection sides — the reverse lookup for
// state.setValue(POWER, targetStrength). Returns (s, false) if s is not wire or power is out of
// range. CITE: DefaultRedstoneWireEvaluator.updatePowerStrength (state.setValue(POWER, targetStrength)).
func RedstoneWireWithPower(s StateID, power int) (StateID, bool) {
	if power < 0 || power > 15 {
		return s, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	w, ok := StateList[s].(RedstoneWire)
	if !ok {
		return s, false
	}
	w.Power = Integer(power)
	sid, ok := ToStateID[w]
	return sid, ok
}

// RedstoneWireStateWith builds the wire state id with the given POWER and four connection sides.
// Used to resolve the state computed by getConnectionState. CITE: RedStoneWireBlock.getConnectionState.
func RedstoneWireStateWith(power int, north, east, south, west RedstoneSide) (StateID, bool) {
	if power < 0 || power > 15 {
		return 0, false
	}
	sid, ok := ToStateID[RedstoneWire{
		North: north, East: east, South: south, West: west, Power: Integer(power),
	}]
	return sid, ok
}

// ShouldConnectTo is RedStoneWireBlock.shouldConnectTo(BlockState, @Nullable Direction) for the
// blocks in the CORE REDSTONE scope. A wire always connects; a signal source connects only when a
// direction is supplied (direction != null). Repeaters/observers are DEFERRED (see server/redstone.go
// scope note) — they are handled by the generic isSignalSource() tail here, which for a repeater/
// observer would be wrong, but neither is a ported signal source yet so the tail is never reached
// for them. CITE: RedStoneWireBlock.shouldConnectTo(state, direction).
//
//	if (state.is(REDSTONE_WIRE)) return true;
//	if (state.is(REPEATER)) return facing==dir || facing.opposite==dir;   // DEFERRED
//	if (state.is(OBSERVER)) return dir == facing;                         // DEFERRED
//	return state.isSignalSource() && direction != null;
//
// isSignalSource(state) is supplied by the caller (server side) since it depends on runtime block
// identity; here we take the precomputed boolean.
func ShouldConnectTo(isWire, isSignalSource, hasDirection bool) bool {
	if isWire {
		return true
	}
	return isSignalSource && hasDirection
}

// RepeaterShouldConnectTo is the RedStoneWireBlock.shouldConnectTo REPEATER special case (checked
// before the generic isSignalSource tail): a wire connects to a repeater at state `s` toward
// `direction` iff the repeater's FACING lies on the queried axis — FACING == direction ||
// FACING.opposite == direction. So a wire connects to a repeater only in front of / behind it (the
// input and output faces), never on the two side faces. Returns (false, false) if s is not a
// repeater (caller falls through to the generic tail). CITE: RedStoneWireBlock.shouldConnectTo
// (`state.is(REPEATER)` branch: `facing == dir || facing.getOpposite() == dir`).
func RepeaterShouldConnectTo(s StateID, direction Direction) (connect bool, isRepeater bool) {
	facing, ok := RepeaterFacing(s)
	if !ok {
		return false, false
	}
	return facing == direction || opposite(facing) == direction, true
}

// opposite is Direction.getOpposite() for the block package's connection logic. CITE:
// Direction.getOpposite.
func opposite(d Direction) Direction {
	switch d {
	case Down:
		return Up
	case Up:
		return Down
	case North:
		return South
	case South:
		return North
	case West:
		return East
	case East:
		return West
	default:
		return d
	}
}

// ---------------------------------------------------------------------------------------------
// Redstone torch (RedstoneTorchBlock / RedstoneWallTorchBlock)
// ---------------------------------------------------------------------------------------------

// IsRedstoneTorch reports whether a state id is a standing redstone torch. CITE: RedstoneTorchBlock.
func IsRedstoneTorch(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(RedstoneTorch)
	return ok
}

// IsRedstoneWallTorch reports whether a state id is a wall-mounted redstone torch. CITE:
// RedstoneWallTorchBlock.
func IsRedstoneWallTorch(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(RedstoneWallTorch)
	return ok
}

// RedstoneTorchLit returns the LIT property of a standing/wall redstone torch, or false if the
// state is neither. CITE: RedstoneTorchBlock.LIT / RedstoneWallTorchBlock.LIT.
func RedstoneTorchLit(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case RedstoneTorch:
		return bool(b.Lit)
	case RedstoneWallTorch:
		return bool(b.Lit)
	default:
		return false
	}
}

// RedstoneWallTorchFacing returns the FACING of a wall redstone torch (the direction the torch head
// points, away from the wall it is mounted on), or (Down, false) if not a wall torch. CITE:
// RedstoneWallTorchBlock.FACING.
func RedstoneWallTorchFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(RedstoneWallTorch); ok {
		return b.Facing, true
	}
	return Down, false
}

// RedstoneTorchWithLit resolves the same torch (standing or wall) with LIT toggled to `lit`,
// preserving FACING for a wall torch. Returns (s, false) if s is not a redstone torch. CITE:
// RedstoneTorchBlock.tick (state.setValue(LIT, ...)).
func RedstoneTorchWithLit(s StateID, lit bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	switch b := StateList[s].(type) {
	case RedstoneTorch:
		b.Lit = Boolean(lit)
		sid, ok := ToStateID[b]
		return sid, ok
	case RedstoneWallTorch:
		b.Lit = Boolean(lit)
		sid, ok := ToStateID[b]
		return sid, ok
	default:
		return s, false
	}
}

// ---------------------------------------------------------------------------------------------
// Redstone block (PoweredBlock) — the constant-15 source
// ---------------------------------------------------------------------------------------------

// IsRedstoneBlock reports whether a state id is the redstone_block (PoweredBlock). It is a constant
// power source (isSignalSource=true, ownSignal=15). CITE: PoweredBlock.
func IsRedstoneBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(RedstoneBlock)
	return ok
}

// ---------------------------------------------------------------------------------------------
// Lever (LeverBlock) and buttons (ButtonBlock)
// ---------------------------------------------------------------------------------------------

// IsLever reports whether a state id is a lever. CITE: LeverBlock.
func IsLever(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Lever)
	return ok
}

// LeverPowered returns the POWERED property of a lever, or false if not a lever. CITE:
// LeverBlock.POWERED.
func LeverPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Lever); ok {
		return bool(b.Powered)
	}
	return false
}

// LeverToggled resolves the lever state with POWERED cycled (state.cycle(POWERED)), preserving
// FACE/FACING. Returns (s, false) if not a lever. CITE: LeverBlock.pull (state.cycle(POWERED)).
func LeverToggled(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Lever); ok {
		b.Powered = !b.Powered
		sid, ok := ToStateID[b]
		return sid, ok
	}
	return s, false
}

// LeverConnectedDirection is FaceAttachedHorizontalDirectionalBlock.getConnectedDirection for a
// lever: CEILING -> DOWN, FLOOR -> UP, WALL -> FACING. This is the single direction the lever emits
// its DIRECT (strong) signal into when powered. Returns (Down, false) if not a lever. CITE:
// FaceAttachedHorizontalDirectionalBlock.getConnectedDirection.
func LeverConnectedDirection(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	b, ok := StateList[s].(Lever)
	if !ok {
		return Down, false
	}
	return connectedDirection(b.Face, b.Facing), true
}

// IsButton reports whether a state id is any button block (stone / wooden / nether / bamboo /
// polished_blackstone). All buttons share the ButtonBlock behaviour and POWERED/FACE/FACING shape.
// CITE: ButtonBlock.
func IsButton(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case StoneButton, PolishedBlackstoneButton,
		OakButton, SpruceButton, BirchButton, JungleButton, AcaciaButton,
		CherryButton, DarkOakButton, PaleOakButton, MangroveButton, BambooButton,
		CrimsonButton, WarpedButton:
		return true
	default:
		return false
	}
}

// ButtonPowered returns the POWERED property of any button, or false if not a button. CITE:
// ButtonBlock.POWERED.
func ButtonPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case StoneButton:
		return bool(b.Powered)
	case PolishedBlackstoneButton:
		return bool(b.Powered)
	case OakButton:
		return bool(b.Powered)
	case SpruceButton:
		return bool(b.Powered)
	case BirchButton:
		return bool(b.Powered)
	case JungleButton:
		return bool(b.Powered)
	case AcaciaButton:
		return bool(b.Powered)
	case CherryButton:
		return bool(b.Powered)
	case DarkOakButton:
		return bool(b.Powered)
	case PaleOakButton:
		return bool(b.Powered)
	case MangroveButton:
		return bool(b.Powered)
	case BambooButton:
		return bool(b.Powered)
	case CrimsonButton:
		return bool(b.Powered)
	case WarpedButton:
		return bool(b.Powered)
	default:
		return false
	}
}

// ButtonWithPowered resolves the button state with POWERED set to `powered`, preserving FACE/FACING.
// Returns (s, false) if not a button. CITE: ButtonBlock.press/checkPressed (state.setValue(POWERED, ...)).
func ButtonWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	p := Boolean(powered)
	switch b := StateList[s].(type) {
	case StoneButton:
		b.Powered = p
		return lookup(b)
	case PolishedBlackstoneButton:
		b.Powered = p
		return lookup(b)
	case OakButton:
		b.Powered = p
		return lookup(b)
	case SpruceButton:
		b.Powered = p
		return lookup(b)
	case BirchButton:
		b.Powered = p
		return lookup(b)
	case JungleButton:
		b.Powered = p
		return lookup(b)
	case AcaciaButton:
		b.Powered = p
		return lookup(b)
	case CherryButton:
		b.Powered = p
		return lookup(b)
	case DarkOakButton:
		b.Powered = p
		return lookup(b)
	case PaleOakButton:
		b.Powered = p
		return lookup(b)
	case MangroveButton:
		b.Powered = p
		return lookup(b)
	case BambooButton:
		b.Powered = p
		return lookup(b)
	case CrimsonButton:
		b.Powered = p
		return lookup(b)
	case WarpedButton:
		b.Powered = p
		return lookup(b)
	default:
		return s, false
	}
}

// ButtonConnectedDirection is getConnectedDirection for a button (same as lever): CEILING -> DOWN,
// FLOOR -> UP, WALL -> FACING — the direction the pressed button emits its DIRECT signal into.
// Returns (Down, false) if not a button. CITE: FaceAttachedHorizontalDirectionalBlock.getConnectedDirection.
func ButtonConnectedDirection(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	face, facing, ok := buttonFaceFacing(s)
	if !ok {
		return Down, false
	}
	return connectedDirection(face, facing), true
}

// ButtonStayPressedTicks is ButtonBlock.ticksToStayPressed — the constructor-supplied unpress delay
// scheduled by press(). Jar-verified from Blocks.<clinit>: STONE and POLISHED_BLACKSTONE buttons =
// 20; every wooden/bamboo/nether button = 30. Returns 0 for a non-button. CITE: ButtonBlock.press
// (level.scheduleTick(pos, this, this.ticksToStayPressed)); Blocks.<clinit> bipush 20 / bipush 30.
func ButtonStayPressedTicks(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0
	}
	switch StateList[s].(type) {
	case StoneButton, PolishedBlackstoneButton:
		return 20 // BlockSetType.STONE / polished_blackstone: bipush 20
	case OakButton, SpruceButton, BirchButton, JungleButton, AcaciaButton,
		CherryButton, DarkOakButton, PaleOakButton, MangroveButton, BambooButton,
		CrimsonButton, WarpedButton:
		return 30 // wooden / bamboo / nether: bipush 30
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------------------------
// Repeater (RepeaterBlock / DiodeBlock) — REDSTONE TIER-2
// ---------------------------------------------------------------------------------------------
//
// Every value and branch below is a literal 1:1 copy of the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar), decompiled via CFR / `javap -c -p` this session:
//   net.minecraft.world.level.block.RepeaterBlock  (DELAY 1..4, LOCKED, POWERED, FACING; getDelay=DELAY*2;
//       useWithoutItem -> state.cycle(DELAY); shouldConnectTo axis rule)
//   net.minecraft.world.level.block.DiodeBlock     (POWERED, FACING; getSignal only out FACING;
//       isSignalSource=true; ownSignal = POWERED ? getOutputSignal : 0)

// IsRepeater reports whether a state id is a repeater (any DELAY/LOCKED/POWERED/FACING combination).
// CITE: RepeaterBlock.
func IsRepeater(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Repeater)
	return ok
}

// RepeaterFacing returns the FACING of a repeater (the direction its INPUT is read from and its
// OUTPUT is emitted toward the OPPOSITE of — DiodeBlock.getSignal emits ownSignal only when the
// queried face == FACING, and getInputSignal reads pos.relative(FACING)). Returns (Down, false) if
// not a repeater. CITE: DiodeBlock.getSignal / getInputSignal (state.getValue(FACING)).
func RepeaterFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(Repeater); ok {
		return b.Facing, true
	}
	return Down, false
}

// RepeaterDelay returns the DELAY property (1..4) of a repeater, or 0 if not a repeater. The tick
// delay is DELAY*2 (2/4/6/8). CITE: RepeaterBlock.getDelay (state.getValue(DELAY) * 2).
func RepeaterDelay(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0
	}
	if b, ok := StateList[s].(Repeater); ok {
		return int(b.Delay)
	}
	return 0
}

// RepeaterLocked returns the LOCKED property of a repeater, or false if not a repeater. CITE:
// RepeaterBlock.LOCKED.
func RepeaterLocked(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Repeater); ok {
		return bool(b.Locked)
	}
	return false
}

// RepeaterPowered returns the POWERED property of a repeater, or false if not a repeater. CITE:
// DiodeBlock.POWERED.
func RepeaterPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Repeater); ok {
		return bool(b.Powered)
	}
	return false
}

// RepeaterWithPowered resolves the same repeater with POWERED set to `powered`, preserving
// DELAY/LOCKED/FACING. Returns (s, false) if not a repeater. CITE: DiodeBlock.tick
// (state.setValue(POWERED, ...)).
func RepeaterWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Repeater); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}

// RepeaterWithLocked resolves the same repeater with LOCKED set to `locked`, preserving
// DELAY/POWERED/FACING. Returns (s, false) if not a repeater. CITE: RepeaterBlock.updateShape /
// getStateForPlacement (state.setValue(LOCKED, ...)).
func RepeaterWithLocked(s StateID, locked bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Repeater); ok {
		b.Locked = Boolean(locked)
		return lookup(b)
	}
	return s, false
}

// RepeaterCycleDelay resolves the repeater with DELAY cycled to the next value (state.cycle(DELAY)):
// DELAY is IntegerProperty.create("delay", 1, 4), so the possible-values list is {1,2,3,4} and cycle
// advances to the next entry, wrapping 4 -> 1. Preserves LOCKED/POWERED/FACING. Returns (s, false)
// if not a repeater. CITE: RepeaterBlock.useWithoutItem (state.cycle(DELAY)); BlockStateProperties.DELAY
// = IntegerProperty.create(1, 4).
func RepeaterCycleDelay(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Repeater); ok {
		if b.Delay >= 4 {
			b.Delay = 1
		} else {
			b.Delay++
		}
		return lookup(b)
	}
	return s, false
}

// ---------------------------------------------------------------------------------------------
// Comparator (ComparatorBlock / DiodeBlock) — REDSTONE TIER-2
// ---------------------------------------------------------------------------------------------
//
//   net.minecraft.world.level.block.ComparatorBlock  (MODE compare/subtract, POWERED, FACING;
//       getDelay=2; useWithoutItem -> state.cycle(MODE); getOutputSignal from the block-entity output)

// IsComparator reports whether a state id is a comparator (any MODE/POWERED/FACING combination).
// CITE: ComparatorBlock.
func IsComparator(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Comparator)
	return ok
}

// ComparatorFacing returns the FACING of a comparator (input read from pos.relative(FACING), output
// emitted toward FACING per DiodeBlock.getSignal). Returns (Down, false) if not a comparator. CITE:
// DiodeBlock.getSignal / getInputSignal.
func ComparatorFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(Comparator); ok {
		return b.Facing, true
	}
	return Down, false
}

// ComparatorGetMode returns the MODE (compare/subtract) of a comparator, or (Compare, false) if not
// a comparator. CITE: ComparatorBlock.MODE (state.getValue(MODE)).
func ComparatorGetMode(s StateID) (ComparatorMode, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return ComparatorModeCompare, false
	}
	if b, ok := StateList[s].(Comparator); ok {
		return b.Mode, true
	}
	return ComparatorModeCompare, false
}

// ComparatorPowered returns the POWERED property of a comparator, or false if not a comparator.
// CITE: DiodeBlock.POWERED.
func ComparatorPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Comparator); ok {
		return bool(b.Powered)
	}
	return false
}

// ComparatorWithPowered resolves the same comparator with POWERED set to `powered`, preserving
// MODE/FACING. Returns (s, false) if not a comparator. CITE: ComparatorBlock.refreshOutputState
// (state.setValue(POWERED, ...)).
func ComparatorWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Comparator); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}

// ComparatorCycleMode resolves the comparator with MODE cycled (state.cycle(MODE)): the enum has two
// values {COMPARE, SUBTRACT}, so cycle toggles COMPARE <-> SUBTRACT. Preserves POWERED/FACING.
// Returns (s, false) if not a comparator. CITE: ComparatorBlock.useWithoutItem (state.cycle(MODE)).
func ComparatorCycleMode(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Comparator); ok {
		if b.Mode == ComparatorModeCompare {
			b.Mode = ComparatorModeSubtract
		} else {
			b.Mode = ComparatorModeCompare
		}
		return lookup(b)
	}
	return s, false
}

// ---------------------------------------------------------------------------------------------
// isRedstoneConductor (the default StatePredicate)
// ---------------------------------------------------------------------------------------------

// IsRedstoneConductor is BlockState.isRedstoneConductor(getter, pos) for the DEFAULT predicate,
// which BlockBehaviour.Properties installs as a direct method reference to
// BlockStateBase.isCollisionShapeFullBlock(getter, pos) (jar-verified: BootstrapMethods #5 for the
// Properties ctor's isRedstoneConductor field is REF_invokeVirtual isCollisionShapeFullBlock). For
// non-dynamic blocks that value is world-context-free, so the baked no-context
// IsCollisionShapeFullBlock table reproduces the live read exactly. A handful of blocks override the
// predicate (e.g. observer, some slabs); none are in the CORE REDSTONE scope, so the default is used.
// CITE: BlockBehaviour$Properties.<init> (isRedstoneConductor = state::isCollisionShapeFullBlock);
// SignalGetter.getSignal / DefaultRedstoneWireEvaluator.getIncomingWireSignal call sites.
func IsRedstoneConductor(s StateID) bool {
	return IsCollisionShapeFullBlock(s)
}

// ---------------------------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------------------------

// connectedDirection is FaceAttachedHorizontalDirectionalBlock.getConnectedDirection(state):
// CEILING -> DOWN, FLOOR -> UP, WALL -> FACING. CITE: FaceAttachedHorizontalDirectionalBlock.
func connectedDirection(face AttachFace, facing Direction) Direction {
	switch face {
	case AttachFaceCeiling:
		return Down
	case AttachFaceFloor:
		return Up
	default: // WALL
		return facing
	}
}

// buttonFaceFacing extracts a button's FACE and FACING. Returns ok=false for a non-button.
func buttonFaceFacing(s StateID) (AttachFace, Direction, bool) {
	switch b := StateList[s].(type) {
	case StoneButton:
		return b.Face, b.Facing, true
	case PolishedBlackstoneButton:
		return b.Face, b.Facing, true
	case OakButton:
		return b.Face, b.Facing, true
	case SpruceButton:
		return b.Face, b.Facing, true
	case BirchButton:
		return b.Face, b.Facing, true
	case JungleButton:
		return b.Face, b.Facing, true
	case AcaciaButton:
		return b.Face, b.Facing, true
	case CherryButton:
		return b.Face, b.Facing, true
	case DarkOakButton:
		return b.Face, b.Facing, true
	case PaleOakButton:
		return b.Face, b.Facing, true
	case MangroveButton:
		return b.Face, b.Facing, true
	case BambooButton:
		return b.Face, b.Facing, true
	case CrimsonButton:
		return b.Face, b.Facing, true
	case WarpedButton:
		return b.Face, b.Facing, true
	default:
		return AttachFaceFloor, Down, false
	}
}

// lookup resolves the state id for a fully-populated block struct via the reverse map.
func lookup(b Block) (StateID, bool) {
	sid, ok := ToStateID[b]
	return sid, ok
}
