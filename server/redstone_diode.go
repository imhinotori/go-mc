package server

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// redstone_diode.go — REDSTONE TIER-2: the 1:1 port of the vanilla DIODE blocks (repeater +
// comparator) from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR /
// `javap -c -p` this session. It EXTENDS the core redstone signal graph in redstone.go: a diode is a
// signal SINK on its input face (reads the graph via getInputSignal) and a signal SOURCE on its
// output face (emits via getSignal only out FACING). Its output flips on a SCHEDULED block tick after
// the diode's delay, wired through block_ticks.go.
//
// CITE (classes/methods, jar-verified this session):
//   net.minecraft.world.level.block.DiodeBlock
//       .getSignal   (ownSignal only when FACING == direction)
//       .getDirectSignal (== getSignal)
//       .ownSignal   (POWERED ? getOutputSignal : 0)
//       .isSignalSource (true)
//       .tick        (isLocked guard; on&&!should -> POWERED=false; !on -> POWERED=true, if !should
//                     scheduleTick(getDelay, VERY_HIGH))
//       .neighborChanged / .checkTickOnNeighbor (schedule a delayed tick when the input state changed)
//       .shouldTurnOn / .getInputSignal (reads the FACING-input face via the graph)
//       .getAlternateSignal / SignalGetter.getControlInputSignal (the two side inputs)
//       .getDelay (abstract), .getOutputSignal (default 15), .sideInputDiodesOnly (default false),
//       .updateNeighborsInFront (notify the output cell + its neighbors except from FACING)
//   net.minecraft.world.level.block.RepeaterBlock
//       .getDelay (DELAY*2 => 2/4/6/8), .isLocked (getAlternateSignal>0), .sideInputDiodesOnly (true),
//       .useWithoutItem (state.cycle(DELAY)), .shouldConnectTo axis rule
//   net.minecraft.world.level.block.ComparatorBlock
//       .getDelay (2), .shouldTurnOn (compare/subtract turn-on), .getInputSignal (adds container /
//       item-frame analog output), .calculateOutputSignal (compare = input, subtract = input-side),
//       .checkTickOnNeighbor (output-value OR turn-on change), .tick -> .refreshOutputState,
//       .useWithoutItem (state.cycle(MODE))
//   net.minecraft.world.level.block.entity.ComparatorBlockEntity (output int; get/setOutputSignal)
//
// SCOPE / DEFERRALS (each jar-cited):
//   - CONTAINER / ITEM-FRAME ANALOG OUTPUT for the comparator (AnalogOutputBlock.getAnalogOutputSignal
//     and ComparatorBlock.getItemFrame): DEFERRED. ComparatorBlock.getInputSignal augments its input
//     with the analog output of the block behind it (a chest/furnace fullness, a cake bites count, an
//     item frame's rotation) when that block hasAnalogOutputSignal() or is a conductor with such a
//     block one step further back. Sulfur has no live comparator/container block-entity seam: chest
//     contents are materialized lazily into openChests only when a player opens the chest, so a chest's
//     fullness is not authoritatively available to a redstone read (see server/chest_open.go
//     resolveChest). The DIRECT-signal input path (wire / redstone_block / lever / button / torch /
//     another diode behind the comparator) is ported FULLY via super.getInputSignal below; only the
//     hasAnalogOutputSignal augmentation is stubbed to "no analog output" (Integer.MIN_VALUE is never
//     substituted, so resultSignal stays the direct input) — the exact vanilla result when the block
//     behind has no analog output. CITE: ComparatorBlock.getInputSignal (targetState.hasAnalogOutputSignal()
//     false branch leaves resultSignal = super.getInputSignal).
//   - PISTON / OBSERVER interactions with diodes: DEFERRED (no piston/observer ported). The
//     movedByPiston neighborChanged/removal branch is not reachable.
//   - EXPERIMENTAL Orientation threading (ExperimentalRedstoneUtils.initialOrientation): the default
//     evaluator ignores Orientation (see redstone.go scope note), so updateNeighborsInFront's orientation
//     arg is dropped — the neighbor notifications it performs are modeled by onRedstoneEdit.
//   - animateTick / particles / sounds (DustParticleOptions, COMPARATOR_CLICK): client cosmetics, no
//     gameplay impact, DEFERRED.

// ---------------------------------------------------------------------------------------------
// direction rotation (Direction.getClockWise / getCounterClockWise, Y-axis, horizontal only)
// ---------------------------------------------------------------------------------------------

// dirClockWise is Direction.getClockWise() (no-arg, rotate around +Y): NORTH->EAST->SOUTH->WEST->NORTH.
// Diodes are always horizontal, so only the four horizontal directions occur. CITE: Direction.getClockWise.
func dirClockWise(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.East
	case block.East:
		return block.South
	case block.South:
		return block.West
	case block.West:
		return block.North
	default:
		return d // vertical: unused by diodes
	}
}

// dirCounterClockWise is Direction.getCounterClockWise() (the inverse of getClockWise):
// NORTH->WEST->SOUTH->EAST->NORTH. CITE: Direction.getCounterClockWise.
func dirCounterClockWise(d block.Direction) block.Direction {
	switch d {
	case block.North:
		return block.West
	case block.West:
		return block.South
	case block.South:
		return block.East
	case block.East:
		return block.North
	default:
		return d
	}
}

// ---------------------------------------------------------------------------------------------
// DiodeBlock getSignal / getDirectSignal (dispatched from redstone.go's stateGetSignal switch)
// ---------------------------------------------------------------------------------------------

// diodeGetSignal is DiodeBlock.getSignal(state, level, pos, direction): the diode emits its
// ownSignal ONLY out its FACING face, else 0. ownSignal = POWERED ? getOutputSignal : 0. For a
// repeater getOutputSignal is a constant 15; for a comparator it is the stored block-entity output
// value. CITE: DiodeBlock.getSignal / DiodeBlock.ownSignal / getOutputSignal.
func (t *TickLoop) diodeGetSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	facing, ok := t.diodeFacing(state)
	if !ok || facing != direction {
		return 0
	}
	return t.diodeOwnSignal(state, pos)
}

// diodeGetDirectSignal is DiodeBlock.getDirectSignal(state, level, pos, direction) == getSignal.
// CITE: DiodeBlock.getDirectSignal (`return state.getSignal(level, pos, direction)`).
func (t *TickLoop) diodeGetDirectSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	return t.diodeGetSignal(state, pos, direction)
}

// diodeOwnSignal is DiodeBlock.ownSignal: POWERED ? getOutputSignal(level,pos,state) : 0. CITE:
// DiodeBlock.ownSignal.
func (t *TickLoop) diodeOwnSignal(state block.StateID, pos pk.Position) int {
	if !t.diodePowered(state) {
		return 0
	}
	return t.diodeGetOutputSignal(state, pos)
}

// diodeGetOutputSignal is the per-subclass getOutputSignal: RepeaterBlock inherits DiodeBlock's
// constant 15; ComparatorBlock returns the stored block-entity output (comparatorOutput map, 0 if
// absent). CITE: DiodeBlock.getOutputSignal (return 15) / ComparatorBlock.getOutputSignal
// (comparatorBlockEntity.getOutputSignal()).
func (t *TickLoop) diodeGetOutputSignal(state block.StateID, pos pk.Position) int {
	if block.IsComparator(state) {
		return t.comparatorStoredOutput(pos)
	}
	return 15 // repeater / DiodeBlock default
}

// diodeFacing returns the FACING of a repeater or comparator; ok=false for a non-diode.
func (t *TickLoop) diodeFacing(state block.StateID) (block.Direction, bool) {
	if block.IsRepeater(state) {
		return block.RepeaterFacing(state)
	}
	if block.IsComparator(state) {
		return block.ComparatorFacing(state)
	}
	return block.Down, false
}

// diodePowered returns the POWERED property of a diode.
func (t *TickLoop) diodePowered(state block.StateID) bool {
	if block.IsRepeater(state) {
		return block.RepeaterPowered(state)
	}
	if block.IsComparator(state) {
		return block.ComparatorPowered(state)
	}
	return false
}

// isDiode is DiodeBlock.isDiode(state): the state is a repeater or comparator. CITE: DiodeBlock.isDiode.
func isDiode(state block.StateID) bool {
	return block.IsRepeater(state) || block.IsComparator(state)
}

// diodeGetDelay is the abstract DiodeBlock.getDelay(state): RepeaterBlock -> DELAY*2 (2/4/6/8),
// ComparatorBlock -> 2. CITE: RepeaterBlock.getDelay (DELAY*2) / ComparatorBlock.getDelay (2).
func diodeGetDelay(state block.StateID) int {
	if block.IsRepeater(state) {
		return block.RepeaterDelay(state) * 2
	}
	if block.IsComparator(state) {
		return 2
	}
	return 0
}

// ---------------------------------------------------------------------------------------------
// input / alternate / lock signal reads
// ---------------------------------------------------------------------------------------------

// diodeGetInputSignal is DiodeBlock.getInputSignal(level, pos, state) for a repeater (and the
// super.getInputSignal a comparator's override starts from):
//
//	Direction direction = state.getValue(FACING);
//	BlockPos targetPos = pos.relative(direction);
//	int input = level.getSignal(targetPos, direction);
//	if (input >= 15) return input;
//	BlockState targetState = level.getBlockState(targetPos);
//	return max(input, targetState.is(REDSTONE_WIRE) ? targetState.getValue(POWER) : 0);
//
// CITE: DiodeBlock.getInputSignal.
func (t *TickLoop) diodeGetInputSignal(state block.StateID, pos pk.Position) int {
	facing, ok := t.diodeFacing(state)
	if !ok {
		return 0
	}
	targetPos := relative(pos, facing)
	input := t.getWeakSignal(targetPos, facing)
	if input >= 15 {
		return input
	}
	targetState := t.redstoneBlockAt(targetPos)
	if w := getWireSignal(targetState); w > input {
		return w
	}
	return input
}

// comparatorGetInputSignal is ComparatorBlock.getInputSignal: super.getInputSignal augmented with the
// analog output of the block behind it.
//
//	int resultSignal = super.getInputSignal(level, pos, state);
//	Direction direction = state.getValue(FACING);
//	BlockPos targetPos = pos.relative(direction);
//	BlockState targetState = level.getBlockState(targetPos);
//	if (targetState.hasAnalogOutputSignal()) {
//	    resultSignal = targetState.getAnalogOutputSignal(level, targetPos, direction.getOpposite());
//	} else if (resultSignal < 15 && targetState.isRedstoneConductor(...)) { ...item-frame... }
//	return resultSignal;
//
// The analog-output source now covers every CONTAINER block-entity (chest/furnace/dispenser/dropper/
// brewing/hopper) via the shared getRedstoneSignalFromContainer fullness formula. The item-frame-behind-
// a-conductor branch stays DEFERRED (no item-frame entity — cited): with no item frame and no analog
// block two-away, resultSignal keeps its super value there, the exact vanilla result. CITE:
// ComparatorBlock.getInputSignal.
func (t *TickLoop) comparatorGetInputSignal(state block.StateID, pos pk.Position) int {
	resultSignal := t.diodeGetInputSignal(state, pos)
	facing, ok := t.diodeFacing(state)
	if !ok {
		return resultSignal
	}
	targetPos := relative(pos, facing)
	// targetState.hasAnalogOutputSignal(): a SCULK SENSOR (hasAnalogOutputSignal true) yields its
	// lastVibrationFrequency while ACTIVE, else 0 -- the comparator reads the FREQUENCY, not the power.
	// CITE: SculkSensorBlock.getAnalogOutputSignal. Checked before the container path (a sensor is not a
	// container).
	if sig, has := t.sculkSensorAnalogOutputSignal(targetPos); has {
		resultSignal = sig
	} else if sig, has := t.crafterAnalogOutputSignal(targetPos); has {
		// targetState.hasAnalogOutputSignal(): true for a CRAFTER; getAnalogOutputSignal ==
		// CrafterBlockEntity.getRedstoneSignal() = the count of grid slots that are non-empty OR disabled
		// (0..9). Checked BEFORE the generic container path: a crafter resolves as a containerView but its
		// analog output is this fill COUNT, NOT the getRedstoneSignalFromContainer fill-ratio. CITE:
		// CrafterBlock.getAnalogOutputSignal -> CrafterBlockEntity.getRedstoneSignal.
		resultSignal = sig
	} else if sig, has := t.containerAnalogOutputSignal(targetPos); has {
		// targetState.hasAnalogOutputSignal(): true for a container block-entity (chest/furnace/dispenser/
		// brewing/hopper — AnalogOutputBlock). getAnalogOutputSignal == getRedstoneSignalFromContainer(container).
		resultSignal = sig
	} else if resultSignal < 15 && block.IsRedstoneConductor(t.redstoneBlockAt(targetPos)) {
		// ComparatorBlock.getInputSignal else-if: the block directly behind is a redstone CONDUCTOR (a full
		// solid), so the comparator reads THROUGH it -- one cell further along FACING it looks for an item
		// frame (facing the same way, i.e. hung on the FAR face of the conductor pointing away from the
		// comparator) AND/OR another analog-output block, taking Math.max of the two (MIN_VALUE when a source
		// is absent). Only if the max is a real value (!= MIN_VALUE) does it override resultSignal. CITE
		// ComparatorBlock.getInputSignal (the isRedstoneConductor two-away frame/analog branch) + getItemFrame.
		twoAway := relative(targetPos, facing)
		const comparatorMinValue = -1 << 31 // Integer.MIN_VALUE sentinel (no source)
		frameOut := comparatorMinValue
		if f := t.comparatorItemFrameBehind(twoAway, facing); f != nil {
			frameOut = f.frameGetAnalogOutput() // ItemFrame.getAnalogOutput
		}
		blockAnalog := comparatorMinValue
		if sig, has := t.sculkSensorAnalogOutputSignal(twoAway); has {
			blockAnalog = sig
		} else if sig, has := t.crafterAnalogOutputSignal(twoAway); has {
			blockAnalog = sig
		} else if sig, has := t.containerAnalogOutputSignal(twoAway); has {
			blockAnalog = sig
		}
		maxAnalog := frameOut
		if blockAnalog > maxAnalog {
			maxAnalog = blockAnalog
		}
		if maxAnalog != comparatorMinValue {
			resultSignal = maxAnalog
		}
	}
	return resultSignal
}

// comparatorItemFrameBehind ports ComparatorBlock.getItemFrame(level, direction, pos): the SINGLE ItemFrame
// occupying the block cell at pos whose getDirection() == direction (the comparator's FACING). Vanilla scans
// getEntitiesOfClass(ItemFrame, AABB(pos .. pos+1), f -> f.getDirection()==direction) and returns the frame
// only when EXACTLY ONE matches (size()==1), else null. frameDirection stores the 3D-data value, which for
// block.Direction is the same ordinal (Down=0,Up=1,North=2,South=3,West=4,East=5), so the facing compare is
// a direct int match. CITE ComparatorBlock.getItemFrame + its lambda (getDirection()==direction).
func (t *TickLoop) comparatorItemFrameBehind(pos pk.Position, facing block.Direction) *Entity {
	cx := float64(pos.X) + 0.5
	cz := float64(pos.Z) + 0.5
	var found *Entity
	count := 0
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if e == nil || !e.isFrame {
			continue
		}
		// AABB(pos, pos+1): the frame's world position must lie within the [pos, pos+1) block cell. The frame
		// center is shifted 0.46875 toward its wall, so it stays inside its own cell -- floor(pos)==pos matches.
		if floorInt(e.x) != pos.X || floorInt(e.y) != pos.Y || floorInt(e.z) != pos.Z {
			continue
		}
		if e.frameDirection != int32(facing) {
			continue // the getDirection()==direction predicate
		}
		found = e
		count++
	}
	if count == 1 {
		return found // size()==1 -> the frame; otherwise (0 or >1) null
	}
	return nil
}

// containerAnalogOutputSignal ports AnalogOutputBlock.getAnalogOutputSignal for a CONTAINER block:
// AbstractContainerMenu.getRedstoneSignalFromContainer(container) == Mth.lerpDiscrete(fillFraction, 0, 15)
// where fillFraction = (sum over non-empty slots of count / getMaxStackSize(itemStack)) / containerSize.
// Returns (signal, true) when a container block-entity resolves at pos, (0, false) otherwise (so the
// comparator keeps its super value). CITE: AbstractContainerMenu.getRedstoneSignalFromContainer (VERIFIED
// javap) + Mth.lerpDiscrete.
func (t *TickLoop) containerAnalogOutputSignal(pos pk.Position) (int, bool) {
	container := t.getContainerAt(pos)
	if container == nil {
		return 0, false
	}
	return getRedstoneSignalFromContainer(container), true
}

// crafterAnalogOutputSignal ports CrafterBlock.getAnalogOutputSignal: if the block at pos is a crafter,
// return (CrafterBlockEntity.getRedstoneSignal(), true) -- the count of grid slots that are non-empty OR
// disabled (0..9). Returns (0, false) when pos is not a crafter (so the comparator falls through to the
// generic container path / its super value). CITE: CrafterBlock.getAnalogOutputSignal.
func (t *TickLoop) crafterAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsCrafter(state) {
		return 0, false
	}
	c := t.resolveCrafter(pos, state)
	if c == nil {
		return 0, false
	}
	return c.crafterGetRedstoneSignal(), true
}

// diodeShouldTurnOn is DiodeBlock.shouldTurnOn(level, pos, state): getInputSignal > 0. A comparator
// overrides this (comparatorShouldTurnOn). CITE: DiodeBlock.shouldTurnOn.
func (t *TickLoop) diodeShouldTurnOn(state block.StateID, pos pk.Position) bool {
	if block.IsComparator(state) {
		return t.comparatorShouldTurnOn(state, pos)
	}
	return t.diodeGetInputSignal(state, pos) > 0
}

// comparatorShouldTurnOn is ComparatorBlock.shouldTurnOn:
//
//	int input = getInputSignal(level, pos, state);
//	if (input == 0) return false;
//	int sideInput = getAlternateSignal(level, pos, state);
//	if (input > sideInput) return true;
//	return input == sideInput && state.getValue(MODE) == COMPARE;
//
// CITE: ComparatorBlock.shouldTurnOn.
func (t *TickLoop) comparatorShouldTurnOn(state block.StateID, pos pk.Position) bool {
	input := t.comparatorGetInputSignal(state, pos)
	if input == 0 {
		return false
	}
	sideInput := t.diodeGetAlternateSignal(state, pos)
	if input > sideInput {
		return true
	}
	mode, _ := block.ComparatorGetMode(state)
	return input == sideInput && mode == block.ComparatorModeCompare
}

// diodeGetAlternateSignal is DiodeBlock.getAlternateSignal(level, pos, state): the max of the two
// side inputs (clockwise / counter-clockwise of FACING), each read via getControlInputSignal with the
// subclass's sideInputDiodesOnly flag (repeater = true, comparator = false). CITE:
// DiodeBlock.getAlternateSignal.
func (t *TickLoop) diodeGetAlternateSignal(state block.StateID, pos pk.Position) int {
	facing, ok := t.diodeFacing(state)
	if !ok {
		return 0
	}
	cw := dirClockWise(facing)
	ccw := dirCounterClockWise(facing)
	diodesOnly := diodeSideInputDiodesOnly(state)
	a := t.getControlInputSignal(relative(pos, cw), cw, diodesOnly)
	b := t.getControlInputSignal(relative(pos, ccw), ccw, diodesOnly)
	if a > b {
		return a
	}
	return b
}

// diodeSideInputDiodesOnly is DiodeBlock.sideInputDiodesOnly(): RepeaterBlock overrides to true (only
// another diode locks / feeds it from the side), ComparatorBlock inherits false (any source feeds the
// side). CITE: RepeaterBlock.sideInputDiodesOnly (true) / DiodeBlock.sideInputDiodesOnly (false).
func diodeSideInputDiodesOnly(state block.StateID) bool {
	return block.IsRepeater(state)
}

// getControlInputSignal is SignalGetter.getControlInputSignal(pos, direction, diodesOnly):
//
//	BlockState s = getBlockState(pos);
//	if (diodesOnly) return DiodeBlock.isDiode(s) ? getDirectSignal(pos, direction) : 0;
//	if (s.is(REDSTONE_BLOCK)) return 15;
//	if (s.is(REDSTONE_WIRE)) return s.getValue(POWER);
//	if (s.isSignalSource()) return getDirectSignal(pos, direction);
//	return 0;
//
// CITE: SignalGetter.getControlInputSignal.
func (t *TickLoop) getControlInputSignal(pos pk.Position, direction block.Direction, diodesOnly bool) int {
	s := t.redstoneBlockAt(pos)
	if diodesOnly {
		if isDiode(s) {
			return t.getDirectSignal(pos, direction)
		}
		return 0
	}
	if block.IsRedstoneBlock(s) {
		return 15
	}
	if block.IsRedstoneWire(s) {
		return block.RedstoneWirePower(s)
	}
	if t.isSignalSource(s) {
		return t.getDirectSignal(pos, direction)
	}
	return 0
}

// diodeIsLocked is DiodeBlock.isLocked / RepeaterBlock.isLocked: the base DiodeBlock is never locked;
// a repeater is locked iff getAlternateSignal > 0 (a powered repeater/comparator facing it from the
// side); a comparator inherits the base (never locked). CITE: DiodeBlock.isLocked (false) /
// RepeaterBlock.isLocked (getAlternateSignal > 0).
func (t *TickLoop) diodeIsLocked(state block.StateID, pos pk.Position) bool {
	if block.IsRepeater(state) {
		return t.diodeGetAlternateSignal(state, pos) > 0
	}
	return false // comparator / DiodeBlock base
}

// diodeShouldPrioritize is DiodeBlock.shouldPrioritize(level, pos, state): the block directly BEHIND
// (pos.relative(FACING.opposite)) is a diode whose FACING is NOT this diode's opposite-facing — i.e. a
// diode pointing INTO this one from the side/perpendicular, which is prioritized to avoid flicker.
// CITE: DiodeBlock.shouldPrioritize.
//
//	Direction direction = state.getValue(FACING).getOpposite();
//	BlockState opp = level.getBlockState(pos.relative(direction));
//	return isDiode(opp) && opp.getValue(FACING) != direction;
func (t *TickLoop) diodeShouldPrioritize(state block.StateID, pos pk.Position) bool {
	facing, ok := t.diodeFacing(state)
	if !ok {
		return false
	}
	direction := dirOpposite(facing)
	oppState := t.redstoneBlockAt(relative(pos, direction))
	if !isDiode(oppState) {
		return false
	}
	oppFacing, _ := t.diodeFacing(oppState)
	return oppFacing != direction
}

// ---------------------------------------------------------------------------------------------
// neighborChanged (checkTickOnNeighbor) — schedule the delayed output flip
// ---------------------------------------------------------------------------------------------

// diodeNeighborChanged is DiodeBlock.neighborChanged: if the diode can still survive, run
// checkTickOnNeighbor (which schedules a delayed tick if the input state changed); otherwise drop +
// remove it and notify all 6 neighbors. The `getBlockState(pos).is(this)` guard is satisfied by the
// caller (drainRedstoneUpdates re-reads and dispatches on the live state). CITE: DiodeBlock.neighborChanged.
func (t *TickLoop) diodeNeighborChanged(pos pk.Position, state block.StateID) {
	if t.diodeCanSurvive(pos) {
		t.diodeCheckTickOnNeighbor(pos, state)
		return
	}
	// canSurvive false: dropResources + removeBlock + updateNeighborsAt(all 6). Mirrors the wire
	// removal path (dropAndRemoveWire). A comparator's stored output is cleared so a re-placed
	// comparator starts at the BE default 0.
	if !t.world().SetBlock(pos, t.airState(), dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, t.airState())
	t.spawnBlockDrop(nil, pos, state)
	t.clearComparatorOutput(pos)
	t.onRedstoneEdit(pos)
}

// diodeCanSurvive is DiodeBlock.canSurvive -> canSurviveOn: the block below can host the diode iff its
// UP face is sturdy with SupportType.RIGID (a full solid, non-fragile top face). CITE:
// DiodeBlock.canSurvive / canSurviveOn (isFaceSturdy(level, belowPos, UP, RIGID)).
func (t *TickLoop) diodeCanSurvive(pos pk.Position) bool {
	below := relative(pos, block.Down)
	return block.IsFaceSturdy(t.redstoneBlockAt(below), block.Up, block.SupportRigid)
}

// diodeCheckTickOnNeighbor is DiodeBlock.checkTickOnNeighbor (with the ComparatorBlock override folded
// in via a type switch):
//
//	RepeaterBlock/base:
//	    if (isLocked) return;
//	    boolean on = POWERED;
//	    boolean should = shouldTurnOn(...);
//	    if (on != should && !willTickThisTick(pos, this)) {
//	        priority = HIGH;
//	        if (shouldPrioritize) priority = EXTREMELY_HIGH; else if (on) priority = VERY_HIGH;
//	        scheduleTick(pos, this, getDelay, priority);
//	    }
//	ComparatorBlock:
//	    if (willTickThisTick(pos, this)) return;
//	    int outputValue = calculateOutputSignal(...);
//	    int oldValue = storedOutput;
//	    if (outputValue != oldValue || POWERED != shouldTurnOn(...)) {
//	        priority = shouldPrioritize ? HIGH : NORMAL;
//	        scheduleTick(pos, this, 2, priority);
//	    }
//
// CITE: DiodeBlock.checkTickOnNeighbor / ComparatorBlock.checkTickOnNeighbor.
func (t *TickLoop) diodeCheckTickOnNeighbor(pos pk.Position, state block.StateID) {
	typ := blockTickType(block.StateList[state].ID())
	if block.IsComparator(state) {
		if t.willTickThisTick(pos, typ) {
			return
		}
		outputValue := t.comparatorCalculateOutputSignal(state, pos)
		oldValue := t.comparatorStoredOutput(pos)
		if outputValue != oldValue || block.ComparatorPowered(state) != t.comparatorShouldTurnOn(state, pos) {
			priority := ticks.PriorityNormal
			if t.diodeShouldPrioritize(state, pos) {
				priority = ticks.PriorityHigh
			}
			t.scheduleBlockTickWithPriority(pos, typ, 2, priority)
		}
		return
	}
	// RepeaterBlock / base DiodeBlock.
	if t.diodeIsLocked(state, pos) {
		return
	}
	on := t.diodePowered(state)
	should := t.diodeShouldTurnOn(state, pos)
	if on == should {
		return
	}
	if t.willTickThisTick(pos, typ) {
		return
	}
	priority := ticks.PriorityHigh
	if t.diodeShouldPrioritize(state, pos) {
		priority = ticks.PriorityExtremelyHigh
	} else if on {
		priority = ticks.PriorityVeryHigh
	}
	t.scheduleBlockTickWithPriority(pos, typ, diodeGetDelay(state), priority)
}

// ---------------------------------------------------------------------------------------------
// scheduled tick (the delayed output flip) — dispatched from block_ticks.go tickBlock
// ---------------------------------------------------------------------------------------------

// repeaterTick is DiodeBlock.tick for a repeater:
//
//	if (isLocked(level, pos, state)) return;
//	boolean on = POWERED;
//	boolean should = shouldTurnOn(...);
//	if (on && !should)  setBlock(pos, POWERED=false, 2);
//	else if (!on) {
//	    setBlock(pos, POWERED=true, 2);
//	    if (!should) scheduleTick(pos, this, getDelay, VERY_HIGH);
//	}
//
// setBlock flag 2 == UPDATE_CLIENTS (no bit-1 neighbor update); the diode's own onPlace-style
// front-notification is DiodeBlock.updateNeighborsInFront, which vanilla runs from onPlace/removal,
// NOT from tick. But the POWERED flip changes the diode's emitted getSignal, so the output cell in
// front must be re-notified for the change to propagate — vanilla achieves this because setBlock with
// flag 2 still calls updateShape and, crucially, the surrounding graph reads the diode's live signal.
// To keep the observable result identical (the front wire/consumer sees the new output), we run
// updateNeighborsInFront after the flip — the exact set of cells DiodeBlock notifies when its output
// changes. CITE: DiodeBlock.tick.
func (t *TickLoop) repeaterTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if t.diodeIsLocked(state, pos) {
		return
	}
	on := block.RepeaterPowered(state)
	should := t.diodeShouldTurnOn(state, pos)
	typ := blockTickType(block.StateList[state].ID())
	if on && !should {
		if newState, ok := block.RepeaterWithPowered(state, false); ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.diodeUpdateNeighborsInFront(newState, pos)
		}
	} else if !on {
		if newState, ok := block.RepeaterWithPowered(state, true); ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.diodeUpdateNeighborsInFront(newState, pos)
		}
		if !should {
			t.scheduleBlockTickWithPriority(pos, typ, diodeGetDelay(state), ticks.PriorityVeryHigh)
		}
	}
}

// comparatorTick is ComparatorBlock.tick -> refreshOutputState. CITE: ComparatorBlock.tick.
func (t *TickLoop) comparatorTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	t.comparatorRefreshOutputState(state, pos)
}

// comparatorRefreshOutputState is ComparatorBlock.refreshOutputState(level, pos, state):
//
//	int outputValue = calculateOutputSignal(...);
//	int oldValue = storedOutput; storedOutput = outputValue;
//	if (oldValue != outputValue || MODE == COMPARE) {
//	    boolean sourceOn = shouldTurnOn(...);
//	    boolean isOn = POWERED;
//	    if (isOn && !sourceOn)      setBlock(pos, POWERED=false, 2);
//	    else if (!isOn && sourceOn) setBlock(pos, POWERED=true, 2);
//	    updateNeighborsInFront(level, pos, state);
//	}
//
// CITE: ComparatorBlock.refreshOutputState.
func (t *TickLoop) comparatorRefreshOutputState(state block.StateID, pos pk.Position) {
	outputValue := t.comparatorCalculateOutputSignal(state, pos)
	oldValue := t.comparatorStoredOutput(pos)
	t.setComparatorOutput(pos, outputValue)
	mode, _ := block.ComparatorGetMode(state)
	if oldValue != outputValue || mode == block.ComparatorModeCompare {
		sourceOn := t.comparatorShouldTurnOn(state, pos)
		isOn := block.ComparatorPowered(state)
		liveState := state
		if isOn && !sourceOn {
			if newState, ok := block.ComparatorWithPowered(state, false); ok && t.world().SetBlock(pos, newState, dimMinY) {
				t.broadcastBlockUpdate(pos, newState)
				liveState = newState
			}
		} else if !isOn && sourceOn {
			if newState, ok := block.ComparatorWithPowered(state, true); ok && t.world().SetBlock(pos, newState, dimMinY) {
				t.broadcastBlockUpdate(pos, newState)
				liveState = newState
			}
		}
		t.diodeUpdateNeighborsInFront(liveState, pos)
	}
}

// comparatorCalculateOutputSignal is ComparatorBlock.calculateOutputSignal(level, pos, state):
//
//	int input = getInputSignal(...);
//	if (input == 0) return 0;
//	int side = getAlternateSignal(...);
//	if (side > input) return 0;
//	return MODE == SUBTRACT ? input - side : input;
//
// CITE: ComparatorBlock.calculateOutputSignal.
func (t *TickLoop) comparatorCalculateOutputSignal(state block.StateID, pos pk.Position) int {
	input := t.comparatorGetInputSignal(state, pos)
	if input == 0 {
		return 0
	}
	side := t.diodeGetAlternateSignal(state, pos)
	if side > input {
		return 0
	}
	mode, _ := block.ComparatorGetMode(state)
	if mode == block.ComparatorModeSubtract {
		return input - side
	}
	return input
}

// ---------------------------------------------------------------------------------------------
// updateNeighborsInFront — notify the output cell + its neighbors (except from FACING)
// ---------------------------------------------------------------------------------------------

// diodeUpdateNeighborsInFront is DiodeBlock.updateNeighborsInFront(level, pos, state):
//
//	Direction direction = state.getValue(FACING);
//	BlockPos oppositePos = pos.relative(direction.getOpposite());
//	level.neighborChanged(oppositePos, this, orientation);
//	level.updateNeighborsAtExceptFromFacing(oppositePos, this, direction, orientation);
//
// The output cell is pos.relative(FACING.opposite) (the front of the diode). Vanilla fires a direct
// neighborChanged at that cell, then updates its neighbors EXCEPT the one back toward the diode
// (`direction` == FACING, from the oppositePos toward the diode). We model both by running the
// redstone neighbor-update dispatch at the output cell and its five other-side neighbors. The
// orientation arg is dropped (default evaluator ignores it — see redstone.go scope note). CITE:
// DiodeBlock.updateNeighborsInFront.
func (t *TickLoop) diodeUpdateNeighborsInFront(state block.StateID, pos pk.Position) {
	facing, ok := t.diodeFacing(state)
	if !ok {
		return
	}
	oppositePos := relative(pos, dirOpposite(facing))
	q := &redstoneUpdateQueue{}
	// neighborChanged(oppositePos, this): the output cell reacts (wire recompute, torch reschedule,
	// another diode's checkTickOnNeighbor).
	q.push(oppositePos)
	// updateNeighborsAtExceptFromFacing(oppositePos, this, direction=FACING): notify the output cell's
	// 6 neighbors EXCEPT the one in the FACING direction from oppositePos (that neighbor is the diode
	// itself at pos — skipping it avoids re-notifying the source, matching vanilla's exclusion).
	for _, d := range redstoneDirs {
		if d == facing {
			continue // the excepted face (points from oppositePos back to the diode at pos)
		}
		q.push(relative(oppositePos, d))
	}
	t.drainRedstoneUpdates(q)
}

// updateNeighbourForOutputSignal ports Level.updateNeighbourForOutputSignal(pos, block): for each of the 4
// HORIZONTAL neighbors of pos, if that neighbor is a COMPARATOR notify it (neighborChanged), else if it is a
// redstone CONDUCTOR recurse ONE step into that conductor's own horizontal neighbors looking for a comparator
// to notify (the read-through-a-solid-block case). This is what an ITEM FRAME calls when its held item /
// rotation changes (setItem / setRotation both invoke updateNeighbourForOutputSignal(pos, AIR)) so a
// comparator reading the frame re-evaluates its output. CITE Level.updateNeighbourForOutputSignal.
func (t *TickLoop) updateNeighbourForOutputSignal(pos pk.Position) {
	if t.world() == nil {
		return
	}
	q := &redstoneUpdateQueue{}
	for _, d := range redstoneHorizontal {
		n := relative(pos, d)
		s := t.redstoneBlockAt(n)
		if block.IsComparator(s) {
			q.push(n) // neighborChanged(comparator): its checkTickOnNeighbor recomputes the output
			continue
		}
		if block.IsRedstoneConductor(s) {
			// read-through: the block behind the conductor (or a frame on its far face) feeds a comparator on
			// the OTHER side, so notify the conductor's horizontal neighbors that are comparators.
			for _, d2 := range redstoneHorizontal {
				nn := relative(n, d2)
				if block.IsComparator(t.redstoneBlockAt(nn)) {
					q.push(nn)
				}
			}
		}
	}
	t.drainRedstoneUpdates(q)
}

// ---------------------------------------------------------------------------------------------
// comparator output storage (ComparatorBlockEntity.output) — region-owned
// ---------------------------------------------------------------------------------------------

// comparatorStoredOutput reads the comparator's stored output value (ComparatorBlockEntity.getOutputSignal),
// defaulting to 0 (the BE's `private int output = 0`) when absent. CITE: ComparatorBlockEntity.getOutputSignal.
func (t *TickLoop) comparatorStoredOutput(pos pk.Position) int {
	m := t.cur().comparatorOutput
	if m == nil {
		return 0
	}
	return m[pos]
}

// setComparatorOutput writes the comparator's output value (ComparatorBlockEntity.setOutputSignal).
// CITE: ComparatorBlockEntity.setOutputSignal.
func (t *TickLoop) setComparatorOutput(pos pk.Position, value int) {
	if t.cur().comparatorOutput == nil {
		t.cur().comparatorOutput = make(map[pk.Position]int)
	}
	t.cur().comparatorOutput[pos] = value
}

// clearComparatorOutput drops a comparator's stored output when the comparator is removed, so a
// re-placed comparator starts at the BE default 0. CITE: removeBlock drops the ComparatorBlockEntity.
func (t *TickLoop) clearComparatorOutput(pos pk.Position) {
	if t.cur().comparatorOutput != nil {
		delete(t.cur().comparatorOutput, pos)
	}
}

// ---------------------------------------------------------------------------------------------
// place / use hooks (DiodeBlock.onPlace / setPlacedBy; Repeater/Comparator.useWithoutItem)
// ---------------------------------------------------------------------------------------------

// NOTE — DiodeBlock.onPlace (updateNeighborsInFront) and setPlacedBy (if shouldTurnOn scheduleTick
// delay 1) on placement are covered by the generic redstone edit hook: the place path
// (block_interact.go reconcileEdit) calls onRedstoneEdit(pos), which enqueues pos itself, and
// drainRedstoneUpdates dispatches the freshly-placed diode through diodeNeighborChanged ->
// checkTickOnNeighbor, scheduling the delayed output flip when its input state changed. The
// diode therefore turns on after its full delay (getDelay) rather than the setPlacedBy delay-1
// fast path; the observable end state (POWERED after the delay, output emitted out FACING) is
// identical, and no diode is left un-scheduled. CITE: DiodeBlock.onPlace / setPlacedBy /
// checkTickOnNeighbor.

// useRepeater is RepeaterBlock.useWithoutItem: cycle DELAY (setBlock flag 3), returns true (the click
// is consumed so no block is placed). Flag 3 == UPDATE_NEIGHBORS|UPDATE_CLIENTS -> broadcast + a
// re-check of the graph around the cell (the delay change can lock/unlock or re-time). CITE:
// RepeaterBlock.useWithoutItem (state.cycle(DELAY), setBlock flag 3).
func (t *TickLoop) useRepeater(pos pk.Position, state block.StateID) bool {
	newState, ok := block.RepeaterCycleDelay(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true // consumed even if the write no-oped (InteractionResult.SUCCESS)
	}
	t.broadcastBlockUpdate(pos, newState)
	// setBlock flag 3's neighbor-update bit: re-run the redstone edit hook so the new LOCKED/timing is
	// re-evaluated (the diode reschedules its own tick via checkTickOnNeighbor on the neighbor pass).
	t.onRedstoneEdit(pos)
	return true
}

// useComparator is ComparatorBlock.useWithoutItem: cycle MODE (setBlock flag 2), then if the block is
// still a comparator refreshOutputState (recompute the output under the new mode). Returns true (click
// consumed). The COMPARATOR_CLICK sound is a client cosmetic, DEFERRED. CITE: ComparatorBlock.useWithoutItem.
func (t *TickLoop) useComparator(pos pk.Position, state block.StateID) bool {
	newState, ok := block.ComparatorCycleMode(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true
	}
	t.broadcastBlockUpdate(pos, newState)
	// `if (level.getBlockState(pos).is(this)) refreshOutputState(...)` — we just wrote newState, so it
	// is a comparator; recompute under the toggled mode.
	if cur := t.redstoneBlockAt(pos); block.IsComparator(cur) {
		t.comparatorRefreshOutputState(cur, pos)
	}
	return true
}

// sculkSensorAnalogOutputSignal ports SculkSensorBlock.getAnalogOutputSignal (hasAnalogOutputSignal ==
// true): (PHASE == ACTIVE) ? SculkSensorBlockEntity.getLastVibrationFrequency() : 0. Returns
// (signal, true) for a sculk sensor (either variant), (0, false) otherwise (so the comparator falls to
// its container/super path). CITE: SculkSensorBlock.getAnalogOutputSignal + hasAnalogOutputSignal.
func (t *TickLoop) sculkSensorAnalogOutputSignal(pos pk.Position) (int, bool) {
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAnySculkSensor(state) {
		return 0, false
	}
	phase, ok := block.SculkSensorPhaseOf(state)
	if !ok || phase != block.SculkSensorPhaseActive {
		return 0, true // ACTIVE-only: not active -> analog 0 (still hasAnalogOutputSignal true).
	}
	be := t.resolveSculkSensor(pos)
	if be == nil {
		return 0, true
	}
	return be.lastVibrationFrequency, true
}
