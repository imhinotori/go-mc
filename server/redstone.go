package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// redstone.go — CORE REDSTONE: the 1:1 port of the vanilla signal graph (wire + torch + block +
// lever + button + the neighbor-signal query core) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar), decompiled via `javap -c -p` / CFR this session. The block-state
// shape (POWER/LIT/POWERED/FACE/FACING/connections) lives in level/block/redstone.go; this file is
// the SERVER half — the runtime signal computation, the scheduled-tick reactions (torch burnout,
// button unpress), and the neighbor-update dispatch that wakes redstone when a cell changes.
//
// CITE (classes/methods, jar-verified):
//   net.minecraft.world.level.SignalGetter.getSignal/getDirectSignal/getDirectSignalTo/
//       getBestNeighborSignal/hasNeighborSignal/hasSignal/getControlInputSignal
//   net.minecraft.world.level.block.RedStoneWireBlock.getSignal/getDirectSignal/ownSignal/
//       isSignalSource/getBlockSignal/neighborChanged/onPlace/updatePowerStrength
//   net.minecraft.world.level.redstone.DefaultRedstoneWireEvaluator.updatePowerStrength/
//       calculateTargetStrength
//   net.minecraft.world.level.redstone.RedstoneWireEvaluator.getBlockSignal/getWireSignal/
//       getIncomingWireSignal
//   net.minecraft.world.level.block.RedstoneTorchBlock.tick/neighborChanged/getSignal/getDirectSignal/
//       ownSignal/hasNeighborSignal  (RECENT_TOGGLE_TIMER=60, MAX_RECENT_TOGGLES=8, RESTART_DELAY=160,
//       TOGGLE_DELAY=2)
//   net.minecraft.world.level.block.RedstoneWallTorchBlock.getSignal/hasNeighborSignal
//   net.minecraft.world.level.block.PoweredBlock.isSignalSource/ownSignal (redstone_block == 15)
//   net.minecraft.world.level.block.LeverBlock.pull/updateNeighbours/getDirectSignal/ownSignal
//   net.minecraft.world.level.block.ButtonBlock.press/checkPressed/tick/getDirectSignal/ownSignal
//
// SCOPE / DEFERRALS (each jar-cited):
//   - REPEATER (net.minecraft.world.level.block.RepeaterBlock/DiodeBlock), COMPARATOR
//     (ComparatorBlock), PISTON (PistonBaseBlock), DISPENSER/DROPPER, RAILS (PoweredRailBlock),
//     DOORS/TRAPDOORS, NOTE BLOCK, redstone LAMP LIGHTING (RedstoneLampBlock changes only its LIT
//     state; the POWER-STATE query below is real, so a lamp WOULD light — only the lamp block's own
//     LIT flip is deferred). These are DEFERRED: not ported as signal sources/sinks here.
//   - The EXPERIMENTAL redstone evaluator (ExperimentalRedstoneWireEvaluator, gated behind
//     FeatureFlags.REDSTONE_EXPERIMENTS which is OFF by default) is NOT ported; vanilla uses the
//     DefaultRedstoneWireEvaluator on a normal world, which is what this file mirrors. CITE:
//     RedStoneWireBlock.useExperimentalEvaluator (== enabledFeatures.contains(REDSTONE_EXPERIMENTS)).
//   - ORIENTATION (net.minecraft.world.level.redstone.Orientation) threading through
//     updateNeighborsAt is an experimental-evaluator optimization; the default evaluator ignores it
//     (updatePowerStrength's orientation arg is unused on the default path), so it is not modeled.
//   - VoxelShape connection visuals / getConnectionState's cross-vs-dot cosmetic promotion affect the
//     wire's RENDERED connection sides and which faces it emits DIRECT signal to. The signal SPREAD
//     itself (getIncomingWireSignal) is connection-independent in vanilla, so wire-to-wire power
//     transfer is exact; the connection-state recompute on shape change is ported for the emitted
//     getSignal faces.

// ---------------------------------------------------------------------------------------------
// direction geometry (block.Direction: Down=0,Up=1,North=2,South=3,West=4,East=5)
// ---------------------------------------------------------------------------------------------

// redstoneDirs is Direction.values() in the vanilla ordinal order (DOWN, UP, NORTH, SOUTH, WEST,
// EAST) — the exact iteration order SignalGetter.getBestNeighborSignal/hasNeighborSignal and
// RedStoneWireBlock.onPlace's Direction.values() loops use. CITE: net.minecraft.core.Direction.values().
var redstoneDirs = [...]block.Direction{block.Down, block.Up, block.North, block.South, block.West, block.East}

// redstoneHorizontal is Direction.Plane.HORIZONTAL in vanilla order (NORTH, SOUTH, WEST, EAST) — the
// order getIncomingWireSignal iterates. Order does not change the max result. CITE:
// Direction.Plane.HORIZONTAL.
var redstoneHorizontal = [...]block.Direction{block.North, block.South, block.West, block.East}

// dirVec returns the unit offset (dx,dy,dz) of a Direction. CITE: Direction.getStepX/Y/Z /
// Direction.getNormal.
func dirVec(d block.Direction) (int, int, int) {
	switch d {
	case block.Down:
		return 0, -1, 0
	case block.Up:
		return 0, 1, 0
	case block.North:
		return 0, 0, -1
	case block.South:
		return 0, 0, 1
	case block.West:
		return -1, 0, 0
	case block.East:
		return 1, 0, 0
	default:
		return 0, 0, 0
	}
}

// dirOpposite is Direction.getOpposite(). CITE: Direction.getOpposite (DOWN<->UP, NORTH<->SOUTH,
// WEST<->EAST).
func dirOpposite(d block.Direction) block.Direction {
	switch d {
	case block.Down:
		return block.Up
	case block.Up:
		return block.Down
	case block.North:
		return block.South
	case block.South:
		return block.North
	case block.West:
		return block.East
	case block.East:
		return block.West
	default:
		return d
	}
}

func relative(p pk.Position, d block.Direction) pk.Position {
	dx, dy, dz := dirVec(d)
	return pk.Position{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
}

// getBlockAt reads the state id at pos; a failed/unloaded read yields air (getBlockState on an
// unloaded chunk returns air in vanilla, which is not a signal source/conductor — the safe
// non-signalling default). CITE: Level.getBlockState.
func (t *TickLoop) redstoneBlockAt(pos pk.Position) block.StateID {
	if t.world() == nil {
		return t.airState()
	}
	if s, ok := t.world().GetBlock(pos, dimMinY); ok {
		return s
	}
	return t.airState()
}

// ---------------------------------------------------------------------------------------------
// SignalGetter — the neighbor-signal query core (any block asks "am I powered?")
// ---------------------------------------------------------------------------------------------

// getWeakSignal is SignalGetter.getSignal(pos, direction): the WEAK signal a block at pos emits
// toward `direction` (the direction FROM the querying cell TO this neighbor). A redstone conductor
// (full solid cube) additionally passes through the strongest DIRECT signal it receives
// (getDirectSignalTo). CITE: SignalGetter.getSignal.
//
//	int signal = state.getSignal(this, pos, direction);
//	if (state.isRedstoneConductor(this, pos)) return max(signal, getDirectSignalTo(pos));
//	return signal;
func (t *TickLoop) getWeakSignal(pos pk.Position, direction block.Direction) int {
	state := t.redstoneBlockAt(pos)
	signal := t.stateGetSignal(state, pos, direction)
	if block.IsRedstoneConductor(state) {
		if d := t.getDirectSignalTo(pos); d > signal {
			return d
		}
	}
	return signal
}

// getDirectSignal is SignalGetter.getDirectSignal(pos, direction): the STRONG (direct) signal a
// block emits toward `direction`. CITE: SignalGetter.getDirectSignal (state.getDirectSignal(...)).
func (t *TickLoop) getDirectSignal(pos pk.Position, direction block.Direction) int {
	return t.stateGetDirectSignal(t.redstoneBlockAt(pos), pos, direction)
}

// getDirectSignalTo is SignalGetter.getDirectSignalTo(pos): the strongest DIRECT signal reaching
// pos from its 6 neighbors, each queried in the direction pointing AWAY from pos into that neighbor.
// Short-circuits at 15. CITE: SignalGetter.getDirectSignalTo (DOWN,UP,NORTH,SOUTH,WEST,EAST order).
func (t *TickLoop) getDirectSignalTo(pos pk.Position) int {
	result := 0
	// The vanilla method hard-codes the order below/above/north/south/west/east, querying the
	// neighbor with the OUTWARD direction (below with DOWN, above with UP, ...).
	for _, d := range redstoneDirs {
		if s := t.getDirectSignal(relative(pos, d), d); s > result {
			result = s
			if result >= 15 {
				return result
			}
		}
	}
	return result
}

// getBestNeighborSignal is SignalGetter.getBestNeighborSignal(pos): the strongest WEAK signal from
// the 6 neighbors, each queried in the direction into that neighbor. Short-circuits at 15. This is
// the "am I powered, and how strongly?" query. CITE: SignalGetter.getBestNeighborSignal.
func (t *TickLoop) getBestNeighborSignal(pos pk.Position) int {
	best := 0
	for _, d := range redstoneDirs {
		signal := t.getWeakSignal(relative(pos, d), d)
		if signal >= 15 {
			return 15
		}
		if signal > best {
			best = signal
		}
	}
	return best
}

// hasNeighborSignal is SignalGetter.hasNeighborSignal(pos): any of the 6 neighbors emits weak
// signal > 0. CITE: SignalGetter.hasNeighborSignal.
func (t *TickLoop) hasNeighborSignal(pos pk.Position) bool {
	for _, d := range redstoneDirs {
		if t.getWeakSignal(relative(pos, d), d) > 0 {
			return true
		}
	}
	return false
}

// hasSignal is SignalGetter.hasSignal(pos, direction): getSignal(pos,direction) > 0. CITE:
// SignalGetter.hasSignal. Used by the torch's attachment test.
func (t *TickLoop) hasSignal(pos pk.Position, direction block.Direction) bool {
	return t.getWeakSignal(pos, direction) > 0
}

// ---------------------------------------------------------------------------------------------
// per-block getSignal / getDirectSignal (BlockState.getSignal/getDirectSignal dispatch)
// ---------------------------------------------------------------------------------------------

// stateGetSignal dispatches BlockState.getSignal(getter, pos, direction) for the ported signal
// sources. The default (BlockBehaviour.getSignal -> ownSignal -> 0) applies to every other block.
// `direction` is the direction the querying neighbor is FROM this block's perspective the face it
// asks about. CITE: the getSignal override in each block class.
func (t *TickLoop) stateGetSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	switch {
	case block.IsRedstoneBlock(state):
		// PoweredBlock: getSignal -> ownSignal == 15 (all faces). CITE: PoweredBlock.ownSignal.
		return 15
	case block.IsLever(state):
		// LeverBlock inherits getSignal -> ownSignal == POWERED ? 15 : 0 (all faces).
		if block.LeverPowered(state) {
			return 15
		}
		return 0
	case block.IsButton(state):
		// ButtonBlock inherits getSignal -> ownSignal == POWERED ? 15 : 0 (all faces).
		if block.ButtonPowered(state) {
			return 15
		}
		return 0
	case block.IsLightningRod(state):
		// LightningRodBlock.ownSignal (getSignal): POWERED ? 15 : 0 out EVERY face. A strike-powered rod
		// is a full 15-out-all-faces source until its 8-tick unpower tick fires. CITE: LightningRodBlock.ownSignal.
		if block.LightningRodPowered(state) {
			return 15
		}
		return 0
	case block.IsRedstoneTorch(state):
		// RedstoneTorchBlock.getSignal: ownSignal (LIT?15:0) out every face EXCEPT UP.
		// CITE: RedstoneTorchBlock.getSignal (`Direction.UP != direction ? ownSignal : 0`).
		if direction == block.Up {
			return 0
		}
		if block.RedstoneTorchLit(state) {
			return 15
		}
		return 0
	case block.IsRedstoneWallTorch(state):
		// RedstoneWallTorchBlock.getSignal: ownSignal out every face EXCEPT FACING.
		// CITE: RedstoneWallTorchBlock.getSignal (`FACING != direction ? ownSignal : 0`).
		if facing, ok := block.RedstoneWallTorchFacing(state); ok && facing == direction {
			return 0
		}
		if block.RedstoneTorchLit(state) {
			return 15
		}
		return 0
	case block.IsRedstoneWire(state):
		return t.wireGetSignal(state, pos, direction)
	case block.IsRepeater(state), block.IsComparator(state):
		// DiodeBlock.getSignal: ownSignal only out FACING (REDSTONE TIER-2, redstone_diode.go).
		return t.diodeGetSignal(state, pos, direction)
	case block.IsObserver(state):
		// ObserverBlock.getSignal: ownSignal (POWERED?15:0) only out FACING (REDSTONE TIER-3, observer.go).
		return observerGetSignal(state, direction)
	case block.IsDetectorRailBlock(state):
		// DetectorRailBlock.ownSignal (getSignal): POWERED ? 15 : 0 out EVERY face — a detector rail with a
		// minecart on it is a full 15-out-all-faces weak source. CITE: DetectorRailBlock.ownSignal.
		if p, ok := block.RailPowered(state); ok && p {
			return 15
		}
		return 0
	default:
		return 0 // BlockBehaviour default: ownSignal == 0
	}
}

// stateGetDirectSignal dispatches BlockState.getDirectSignal(getter, pos, direction). Default is 0.
// CITE: the getDirectSignal override in each block class.
func (t *TickLoop) stateGetDirectSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	switch {
	case block.IsLever(state):
		// LeverBlock.getDirectSignal: 15 iff POWERED && getConnectedDirection == direction.
		if block.LeverPowered(state) {
			if cd, ok := block.LeverConnectedDirection(state); ok && cd == direction {
				return 15
			}
		}
		return 0
	case block.IsButton(state):
		// ButtonBlock.getDirectSignal: 15 iff POWERED && getConnectedDirection == direction.
		if block.ButtonPowered(state) {
			if cd, ok := block.ButtonConnectedDirection(state); ok && cd == direction {
				return 15
			}
		}
		return 0
	case block.IsLightningRod(state):
		// LightningRodBlock.getDirectSignal: 15 iff POWERED && FACING == direction. Unlike the lever/button
		// (which use getConnectedDirection off FACE/FACING), the rod emits its STRONG signal straight out its
		// FACING. CITE: LightningRodBlock.getDirectSignal (`state.getValue(FACING) == direction`).
		if block.LightningRodPowered(state) {
			if facing, ok := block.LightningRodFacing(state); ok && facing == direction {
				return 15
			}
		}
		return 0
	case block.IsRedstoneTorch(state), block.IsRedstoneWallTorch(state):
		// RedstoneTorchBlock.getDirectSignal: getSignal only for DOWN, else 0 (a torch emits strong
		// power only straight down into its attachment cell). CITE: RedstoneTorchBlock.getDirectSignal.
		if direction == block.Down {
			return t.stateGetSignal(state, pos, direction)
		}
		return 0
	case block.IsRedstoneWire(state):
		return t.wireGetDirectSignal(state, pos, direction)
	case block.IsRepeater(state), block.IsComparator(state):
		// DiodeBlock.getDirectSignal == getSignal (REDSTONE TIER-2, redstone_diode.go).
		return t.diodeGetDirectSignal(state, pos, direction)
	case block.IsObserver(state):
		// ObserverBlock.getDirectSignal == getSignal (REDSTONE TIER-3, observer.go).
		return observerGetSignal(state, direction)
	case block.IsDetectorRailBlock(state):
		// DetectorRailBlock.getDirectSignal: POWERED && direction == UP ? 15 : 0 (the strong signal a detector
		// rail emits straight UP into the block above). CITE: DetectorRailBlock.getDirectSignal.
		if p, ok := block.RailPowered(state); ok && p && direction == block.Up {
			return 15
		}
		return 0
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------------------------
// Redstone wire — signal emission + power computation (DefaultRedstoneWireEvaluator)
// ---------------------------------------------------------------------------------------------
//
// RedStoneWireBlock.shouldSignal is a mutable field, TRUE except during getBlockSignal (where it is
// briefly set false so the wire does not count its own emission when reading the block signal). We
// model that by passing an explicit `shouldSignal` boolean into the wire's getSignal/getDirectSignal
// and by computing getBlockSignal with shouldSignal=false. CITE: RedStoneWireBlock.shouldSignal /
// getBlockSignal (`shouldSignal=false; r=getBestNeighborSignal; shouldSignal=true; return r`).

// wireGetSignal is RedStoneWireBlock.getSignal with shouldSignal TRUE (the normal external query).
// CITE: RedStoneWireBlock.getSignal.
func (t *TickLoop) wireGetSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	return t.wireGetSignalShould(state, pos, direction, true)
}

// wireGetSignalShould is RedStoneWireBlock.getSignal parameterized on shouldSignal:
//
//	if (!shouldSignal || direction == DOWN) return 0;
//	int own = ownSignal(state);                        // POWER value
//	if (own == 0) return 0;
//	if (direction == UP
//	 || getConnectionState(...).getValue(PROPERTY_BY_DIRECTION[direction.opposite]).isConnected())
//	     return own;
//	return 0;
//
// i.e. the wire emits its POWER upward and toward any horizontal face it is CONNECTED to (checked
// against the opposite side's connection property of the freshly recomputed connection state).
// CITE: RedStoneWireBlock.getSignal.
func (t *TickLoop) wireGetSignalShould(state block.StateID, pos pk.Position, direction block.Direction, shouldSignal bool) int {
	if !shouldSignal || direction == block.Down {
		return 0
	}
	own := block.RedstoneWirePower(state)
	if own <= 0 {
		return 0
	}
	if direction == block.Up {
		return own
	}
	// getConnectionState(level, state, pos).getValue(PROPERTY_BY_DIRECTION.get(opposite)).isConnected()
	conn := t.wireConnectionState(state, pos)
	if t.wireSideConnected(conn, dirOpposite(direction)) {
		return own
	}
	return 0
}

// wireGetDirectSignal is RedStoneWireBlock.getDirectSignal: 0 unless shouldSignal, then delegates to
// getSignal (via BlockState.getSignal). With shouldSignal TRUE this equals wireGetSignal. CITE:
// RedStoneWireBlock.getDirectSignal.
func (t *TickLoop) wireGetDirectSignal(state block.StateID, pos pk.Position, direction block.Direction) int {
	return t.wireGetSignal(state, pos, direction)
}

// wireSideConnected reads whether the recomputed connection state is connected on `dir` (one of the
// four horizontal directions). UP/DOWN are never wire side properties; connection is only tracked
// N/E/S/W. CITE: RedStoneWireBlock.PROPERTY_BY_DIRECTION (horizontal-only).
func (t *TickLoop) wireSideConnected(conn block.StateID, dir block.Direction) bool {
	north, east, south, west := block.RedstoneWireConnections(conn)
	switch dir {
	case block.North:
		return north.IsConnected()
	case block.East:
		return east.IsConnected()
	case block.South:
		return south.IsConnected()
	case block.West:
		return west.IsConnected()
	default:
		return false
	}
}

// getBlockSignal is RedStoneWireBlock.getBlockSignal(level, pos): the strongest NON-wire signal
// reaching the wire cell — computed with shouldSignal=false so the wire ignores its own emission.
// This is `getBestNeighborSignal(pos)` under shouldSignal=false. Because our getWeakSignal/
// stateGetSignal path routes a wire neighbor through wireGetSignalShould, we thread the false flag
// only for the CENTER wire whose block signal we are reading; neighbor wires are queried normally
// (their POWER is read by the evaluator's getWireSignal, not here). Vanilla's shouldSignal is a
// field on the SINGLE RedStoneWireBlock singleton, so setting it false suppresses EVERY wire's
// getSignal during the getBestNeighborSignal scan. We reproduce that by passing shouldSignal=false
// through the whole scan for this call. CITE: RedStoneWireBlock.getBlockSignal /
// RedstoneWireEvaluator.getBlockSignal.
func (t *TickLoop) getBlockSignal(pos pk.Position) int {
	best := 0
	for _, d := range redstoneDirs {
		signal := t.getWeakSignalShould(relative(pos, d), d, false)
		if signal >= 15 {
			return 15
		}
		if signal > best {
			best = signal
		}
	}
	return best
}

// getWeakSignalShould is getWeakSignal with the wire shouldSignal flag threaded through — used only
// by getBlockSignal (shouldSignal=false). A wire neighbor returns 0 (its getSignal is suppressed);
// non-wire sources are unaffected. CITE: RedStoneWireBlock.getBlockSignal scan under shouldSignal=false.
func (t *TickLoop) getWeakSignalShould(pos pk.Position, direction block.Direction, shouldSignal bool) int {
	state := t.redstoneBlockAt(pos)
	var signal int
	if block.IsRedstoneWire(state) {
		signal = t.wireGetSignalShould(state, pos, direction, shouldSignal)
	} else {
		signal = t.stateGetSignal(state, pos, direction)
	}
	if block.IsRedstoneConductor(state) {
		if d := t.getDirectSignalToShould(pos, shouldSignal); d > signal {
			return d
		}
	}
	return signal
}

// getDirectSignalToShould is getDirectSignalTo with the wire shouldSignal flag threaded through.
func (t *TickLoop) getDirectSignalToShould(pos pk.Position, shouldSignal bool) int {
	result := 0
	for _, d := range redstoneDirs {
		np := relative(pos, d)
		ns := t.redstoneBlockAt(np)
		var s int
		if block.IsRedstoneWire(ns) {
			s = t.wireGetSignalShould(ns, np, d, shouldSignal) // getDirectSignal == getSignal for wire
		} else {
			s = t.stateGetDirectSignal(ns, np, d)
		}
		if s > result {
			result = s
			if result >= 15 {
				return result
			}
		}
	}
	return result
}

// getWireSignal is RedstoneWireEvaluator.getWireSignal(pos, state): the POWER of a wire neighbor,
// or 0 if the neighbor is not wire. CITE: RedstoneWireEvaluator.getWireSignal (`state.is(wireBlock)
// ? state.getValue(POWER) : 0`).
func getWireSignal(state block.StateID) int {
	if block.IsRedstoneWire(state) {
		return block.RedstoneWirePower(state)
	}
	return 0
}

// getIncomingWireSignal is RedstoneWireEvaluator.getIncomingWireSignal(level, pos): the strongest
// wire POWER reaching this cell from adjacent wires — including the up/down "step" connections over
// solid conductors and under non-conductors — then decremented by 1 (min 0). This is the
// max-neighbor-minus-1 spread. CITE: RedstoneWireEvaluator.getIncomingWireSignal.
//
//	int signal = 0;
//	for (Direction d : HORIZONTAL) {
//	    BlockPos n = pos.relative(d); BlockState ns = getBlockState(n);
//	    signal = max(signal, getWireSignal(n, ns));
//	    BlockPos up = pos.above();
//	    if (ns.isRedstoneConductor(n) && !getBlockState(up).isRedstoneConductor(up)) {
//	        BlockPos nu = n.above(); signal = max(signal, getWireSignal(nu, getBlockState(nu)));
//	    } else if (!ns.isRedstoneConductor(n)) {
//	        BlockPos nb = n.below(); signal = max(signal, getWireSignal(nb, getBlockState(nb)));
//	    }
//	}
//	return max(0, signal - 1);
func (t *TickLoop) getIncomingWireSignal(pos pk.Position) int {
	signal := 0
	up := relative(pos, block.Up)
	upConductor := block.IsRedstoneConductor(t.redstoneBlockAt(up))
	for _, d := range redstoneHorizontal {
		n := relative(pos, d)
		ns := t.redstoneBlockAt(n)
		if w := getWireSignal(ns); w > signal {
			signal = w
		}
		nConductor := block.IsRedstoneConductor(ns)
		if nConductor {
			if !upConductor {
				// step UP: read the wire diagonally up-and-over the solid neighbor.
				nu := relative(n, block.Up)
				if w := getWireSignal(t.redstoneBlockAt(nu)); w > signal {
					signal = w
				}
			}
			// neighbor is a conductor and up is blocked: no step (vanilla `continue`).
		} else {
			// step DOWN: read the wire diagonally down-and-under the non-solid neighbor.
			nb := relative(n, block.Down)
			if w := getWireSignal(t.redstoneBlockAt(nb)); w > signal {
				signal = w
			}
		}
	}
	if signal-1 > 0 {
		return signal - 1
	}
	return 0
}

// calculateTargetStrength is DefaultRedstoneWireEvaluator.calculateTargetStrength(level, pos):
//
//	int blockSignal = getBlockSignal(level, pos);
//	if (blockSignal == 15) return 15;
//	return max(blockSignal, getIncomingWireSignal(level, pos));
//
// CITE: DefaultRedstoneWireEvaluator.calculateTargetStrength.
func (t *TickLoop) calculateTargetStrength(pos pk.Position) int {
	blockSignal := t.getBlockSignal(pos)
	if blockSignal == 15 {
		return 15
	}
	if inc := t.getIncomingWireSignal(pos); inc > blockSignal {
		return inc
	}
	return blockSignal
}

// updateWirePowerStrength is DefaultRedstoneWireEvaluator.updatePowerStrength(level, pos, state):
//
//	int target = calculateTargetStrength(level, pos);
//	if (state.getValue(POWER) != target) {
//	    if (level.getBlockState(pos) == state) level.setBlock(pos, state.setValue(POWER, target), 2);
//	    Set<BlockPos> toUpdate = {pos} ∪ {pos.relative(d) : d in Direction.values()};
//	    for (BlockPos p : toUpdate) level.updateNeighborsAt(p, wireBlock);
//	}
//
// The neighbor-update set (self + 6) is what cascades the recompute across a wire network; we drive
// it through the redstone neighbor-update worklist so a chain of wires converges to its fixpoint in
// the same observable order. `changed` reports whether the POWER changed (so the caller can enqueue
// the neighbor updates). CITE: DefaultRedstoneWireEvaluator.updatePowerStrength.
func (t *TickLoop) updateWirePowerStrength(pos pk.Position, state block.StateID, q *redstoneUpdateQueue) {
	target := t.calculateTargetStrength(pos)
	if block.RedstoneWirePower(state) == target {
		return
	}
	// level.getBlockState(pos) == state guard: only rewrite if the cell is still this exact state.
	if cur := t.redstoneBlockAt(pos); cur == state {
		if newState, ok := block.RedstoneWireWithPower(state, target); ok {
			if t.world().SetBlock(pos, newState, dimMinY) {
				t.broadcastBlockUpdate(pos, newState)
			}
		}
	}
	// updateNeighborsAt for the set {pos} ∪ {pos.relative(d)} — enqueue each so neighboring wires
	// recompute (their neighborChanged -> updatePowerStrength). Self is included so a wire that just
	// changed re-notifies the cross-shaped neighborhood, exactly as the HashSet does.
	q.push(pos)
	for _, d := range redstoneDirs {
		q.push(relative(pos, d))
	}
	// updateNeighborsOfNeighboringWires: a wire whose POWER changed also wakes the DIAGONAL corner
	// wires it connects to across a solid/non-solid step (a staircase). Vanilla runs this only on
	// onPlace/removal (checkCornerChangeAt), but a signal CHANGE must reach those same corner wires or
	// a stepped run never updates. Enqueuing the corner cells here reproduces the identical observable
	// result (each corner wire recomputes via getIncomingWireSignal) without a separate ShapeUpdates
	// queue. CITE: RedStoneWireBlock.updateNeighborsOfNeighboringWires / checkCornerChangeAt.
	t.enqueueCornerWires(pos, q)
}

// enqueueCornerWires is the RedStoneWireBlock.updateNeighborsOfNeighboringWires corner set for the
// wire at pos: for each horizontal direction, the cell one step up (if the horizontal neighbor is a
// redstone conductor) or one step down (otherwise), diagonally — the cells a wire can step to.
// CITE: RedStoneWireBlock.updateNeighborsOfNeighboringWires.
//
//	for (Direction d : HORIZONTAL) {
//	    BlockPos target = pos.relative(d);
//	    if (getBlockState(target).isRedstoneConductor(target)) checkCornerChangeAt(target.above());
//	    else checkCornerChangeAt(target.below());
//	}
//
// checkCornerChangeAt(p) is a no-op unless p is a wire, in which case it re-notifies p's
// neighbourhood — modeled here by enqueuing p (the worklist re-runs its neighborChanged).
func (t *TickLoop) enqueueCornerWires(pos pk.Position, q *redstoneUpdateQueue) {
	for _, d := range redstoneHorizontal {
		target := relative(pos, d)
		if block.IsRedstoneConductor(t.redstoneBlockAt(target)) {
			q.push(relative(target, block.Up))
		} else {
			q.push(relative(target, block.Down))
		}
	}
}

// ---------------------------------------------------------------------------------------------
// wire connection state (getConnectionState / getConnectingSide / shouldConnectTo)
// ---------------------------------------------------------------------------------------------

// wireConnectionState is RedStoneWireBlock.getConnectionState(level, state, pos) restricted to the
// connection-side computation (the POWER is carried through unchanged). It recomputes the four
// RedstoneSide connections for the wire at pos from its surroundings, then applies the cross/dot
// cosmetic promotion. This drives which FACES the wire emits signal to (wireGetSignal). CITE:
// RedStoneWireBlock.getConnectionState / getMissingConnections.
func (t *TickLoop) wireConnectionState(state block.StateID, pos pk.Position) block.StateID {
	power := block.RedstoneWirePower(state)
	if power < 0 {
		return state
	}
	// getMissingConnections computes each side via getConnectingSide; we compute all four directly
	// (the incremental getMissingConnections recursion converges to the same four sides).
	north := t.wireConnectingSide(pos, block.North)
	east := t.wireConnectingSide(pos, block.East)
	south := t.wireConnectingSide(pos, block.South)
	west := t.wireConnectingSide(pos, block.West)

	// getConnectionState cross/dot promotion: if the wire is a dot (no connections) it stays a dot;
	// otherwise a side with no connection that has an empty perpendicular axis is promoted to SIDE.
	// CITE: RedStoneWireBlock.getConnectionState.
	nConn := north.IsConnected()
	eConn := east.IsConnected()
	sConn := south.IsConnected()
	wConn := west.IsConnected()
	if !nConn && !eConn && !sConn && !wConn {
		// wasDot && isDot -> unchanged (all NONE).
		if sid, ok := block.RedstoneWireStateWith(power, north, east, south, west); ok {
			return sid
		}
		return state
	}
	northSouthEmpty := !nConn && !sConn
	eastWestEmpty := !eConn && !wConn
	if !wConn && northSouthEmpty {
		west = block.RedstoneSideSide
	}
	if !eConn && northSouthEmpty {
		east = block.RedstoneSideSide
	}
	if !nConn && eastWestEmpty {
		north = block.RedstoneSideSide
	}
	if !sConn && eastWestEmpty {
		south = block.RedstoneSideSide
	}
	if sid, ok := block.RedstoneWireStateWith(power, north, east, south, west); ok {
		return sid
	}
	return state
}

// wireConnectingSide is RedStoneWireBlock.getConnectingSide(level, pos, direction): whether (and
// how) the wire connects toward `direction`. CITE: RedStoneWireBlock.getConnectingSide.
//
//	boolean canConnectUp = !getBlockState(pos.above()).isRedstoneConductor(pos.above());
//	BlockPos rel = pos.relative(direction); BlockState relState = getBlockState(rel);
//	if (canConnectUp) {
//	    boolean placeableAbove = relState is TrapDoor || canSurviveOn(rel, relState);
//	    if (placeableAbove && shouldConnectTo(getBlockState(rel.above()))) {
//	        return relState.isFaceSturdy(rel, direction.opposite) ? UP : SIDE;
//	    }
//	}
//	if (shouldConnectTo(relState, direction)
//	 || (!relState.isRedstoneConductor(rel) && shouldConnectTo(getBlockState(rel.below()))))
//	     return SIDE;
//	return NONE;
func (t *TickLoop) wireConnectingSide(pos pk.Position, direction block.Direction) block.RedstoneSide {
	above := relative(pos, block.Up)
	canConnectUp := !block.IsRedstoneConductor(t.redstoneBlockAt(above))

	rel := relative(pos, direction)
	relState := t.redstoneBlockAt(rel)

	if canConnectUp {
		// TrapDoorBlock instance is DEFERRED (no trapdoors ported); canSurviveOn == face-sturdy UP.
		placeableAbove := t.wireCanSurviveOn(rel, relState)
		if placeableAbove {
			relAbove := relative(rel, block.Up)
			// shouldConnectTo(getBlockState(rel.above())) — the 1-arg (direction == null) overload.
			if t.shouldConnectToNull(t.redstoneBlockAt(relAbove)) {
				// relState.isFaceSturdy(direction.opposite) ? UP : SIDE.
				if block.IsFaceSturdy(relState, dirOpposite(direction), block.SupportFull) {
					return block.RedstoneSideUp
				}
				return block.RedstoneSideSide
			}
		}
	}
	// shouldConnectTo(relState, direction) — the 2-arg overload WITH the direction (so the repeater
	// axis rule uses the real direction); the .below() call is the 1-arg (null) overload.
	if t.shouldConnectToDir(relState, direction, true /*hasDirection*/) ||
		(!block.IsRedstoneConductor(relState) && t.shouldConnectToNull(t.redstoneBlockAt(relative(rel, block.Down)))) {
		return block.RedstoneSideSide
	}
	return block.RedstoneSideNone
}

// shouldConnectToNull is RedStoneWireBlock.shouldConnectTo(state) — the 1-arg overload that passes a
// null direction. A repeater with a null direction never connects (facing == null || facing.opposite
// == null are both false); the generic tail requires direction != null so it is false too; only a wire
// connects. CITE: RedStoneWireBlock.shouldConnectTo(state) (delegates to (state, null)).
func (t *TickLoop) shouldConnectToNull(state block.StateID) bool {
	if block.IsRedstoneWire(state) {
		return true
	}
	// A repeater under the null-direction overload: facing == null is false, facing.opposite == null is
	// false -> no connection. The generic isSignalSource && (direction != null) tail is also false.
	return false
}

// shouldConnectTo is RedStoneWireBlock.shouldConnectTo(state, direction) for the ported blocks: a
// wire always connects; a signal source connects when a direction is supplied. Repeaters/observers
// are DEFERRED. `hasDirection` mirrors the `direction != null` distinction (the 1-arg overload
// passes null). CITE: RedStoneWireBlock.shouldConnectTo.
func (t *TickLoop) shouldConnectTo(state block.StateID, hasDirection bool) bool {
	return t.shouldConnectToDir(state, block.Down, hasDirection)
}

// shouldConnectToDir is RedStoneWireBlock.shouldConnectTo(state, direction) with the REPEATER axis
// special case wired in: a wire connects to a repeater only in front/behind it (FACING == dir ||
// FACING.opposite == dir), not on its two side faces. When `hasDirection` is false (the 1-arg overload
// passes null), the repeater special case still evaluates against `direction` but that branch is only
// reached from getConnectingSide's direction-bearing call. CITE: RedStoneWireBlock.shouldConnectTo
// (REPEATER branch; OBSERVER branch dir==FACING; generic isSignalSource && direction != null tail).
func (t *TickLoop) shouldConnectToDir(state block.StateID, direction block.Direction, hasDirection bool) bool {
	if block.IsRedstoneWire(state) {
		return true
	}
	// REPEATER: FACING == direction || FACING.opposite == direction. This branch is independent of
	// hasDirection in vanilla (it does not consult direction != null), so a repeater connects on its
	// axis regardless. CITE: RedStoneWireBlock.shouldConnectTo REPEATER branch.
	if connect, isRepeater := block.RepeaterShouldConnectTo(state, direction); isRepeater {
		return connect
	}
	// OBSERVER: direction == FACING (a wire connects only to the observer's output face). Independent of
	// hasDirection in vanilla, matching the REPEATER branch. CITE: RedStoneWireBlock.shouldConnectTo
	// OBSERVER branch (REDSTONE TIER-3).
	if connect, isObserver := block.ObserverShouldConnectTo(state, direction); isObserver {
		return connect
	}
	// Generic tail: isSignalSource() && direction != null. Comparator falls here (connects on any side
	// when a direction is supplied), matching vanilla (no comparator special case in shouldConnectTo).
	return t.isSignalSource(state) && hasDirection
}

// isSignalSource is BlockState.isSignalSource() for the ported blocks. Wire's isSignalSource is
// shouldSignal (TRUE outside getBlockSignal), so a wire counts as a source for connection purposes.
// CITE: the isSignalSource override per block class.
func (t *TickLoop) isSignalSource(state block.StateID) bool {
	switch {
	case block.IsRedstoneBlock(state), block.IsLever(state), block.IsButton(state),
		block.IsRedstoneTorch(state), block.IsRedstoneWallTorch(state), block.IsRedstoneWire(state),
		block.IsLightningRod(state),
		block.IsRepeater(state), block.IsComparator(state), block.IsObserver(state):
		// DiodeBlock.isSignalSource == true (REDSTONE TIER-2); ObserverBlock.isSignalSource == true
		// (REDSTONE TIER-3); LightningRodBlock.isSignalSource == true. CITE: DiodeBlock.isSignalSource /
		// ObserverBlock.isSignalSource / LightningRodBlock.isSignalSource.
		return true
	default:
		return false
	}
}

// wireCanSurviveOn is RedStoneWireBlock.canSurviveOn(level, pos, state): the block can host a wire
// on top iff its UP face is sturdy OR it is a hopper. Hopper is DEFERRED as a special-case (the
// face-sturdy check covers normal ground; a hopper is not yet ported as a redstone host). CITE:
// RedStoneWireBlock.canSurviveOn (`isFaceSturdy(UP) || is(HOPPER)`).
func (t *TickLoop) wireCanSurviveOn(pos pk.Position, state block.StateID) bool {
	return block.IsFaceSturdy(state, block.Up, block.SupportFull)
}

// ---------------------------------------------------------------------------------------------
// neighbor-update dispatch (Level.updateNeighborsAt -> neighborChanged) for redstone consumers
// ---------------------------------------------------------------------------------------------

// redstoneUpdateQueue is a de-duplicating FIFO worklist for the wire recompute cascade. Vanilla's
// updateNeighborsAt fires synchronous neighborChanged calls that re-enter updatePowerStrength; a
// large wire network converges by re-notifying the cross-shaped neighborhood until POWER stabilizes.
// A worklist reproduces that fixpoint without unbounded Go stack recursion, visiting the same set of
// cells. The bound guards against a pathological loop (vanilla relies on POWER monotonically
// settling; a cell whose POWER did not change enqueues nothing further).
type redstoneUpdateQueue struct {
	items []pk.Position
}

func (q *redstoneUpdateQueue) push(p pk.Position) { q.items = append(q.items, p) }

func (q *redstoneUpdateQueue) pop() (pk.Position, bool) {
	if len(q.items) == 0 {
		return pk.Position{}, false
	}
	p := q.items[0]
	q.items = q.items[1:]
	return p, true
}

// redstoneUpdateBudget bounds the total neighborChanged dispatches per edit so a mis-modeled loop
// cannot spin forever. A realistic wire network converges in far fewer steps (each wire's POWER
// settles monotonically once its incoming maximum is fixed). This is an anti-runaway guard, not a
// gameplay knob; the fixpoint is reached well within it.
const redstoneUpdateBudget = 1 << 16

// onRedstoneEdit is the redstone slice of Level.updateNeighborsAt(pos): after the cell at pos
// changes (a place/break, a wire POWER update, a torch/lever/button state flip), notify each of the
// 6 neighbors so any redstone consumer there reacts (wire recompute, torch reschedule). Drives the
// worklist to its fixpoint. Also handles pos itself when it is a wire that must recompute on an
// onPlace-style change. CITE: Level.updateNeighborsAt / block neighborChanged.
func (t *TickLoop) onRedstoneEdit(pos pk.Position) {
	if t.world() == nil {
		return
	}
	q := &redstoneUpdateQueue{}
	// updateNeighborsAt(pos): notify the 6 neighbors of pos.
	for _, d := range redstoneDirs {
		q.push(relative(pos, d))
	}
	// A wire freshly placed/changed at pos also recomputes its own strength (onPlace ->
	// updatePowerStrength(pos)) — enqueue pos so its wire (if any) reacts.
	q.push(pos)
	t.drainRedstoneUpdates(q)
}

// drainRedstoneUpdates processes the neighbor-update worklist: for each queued position, dispatch
// the redstone neighborChanged reaction of the block there. Wire recompute pushes more positions;
// the loop converges when POWER stops changing. CITE: neighborChanged dispatch.
func (t *TickLoop) drainRedstoneUpdates(q *redstoneUpdateQueue) {
	budget := redstoneUpdateBudget
	for {
		pos, ok := q.pop()
		if !ok {
			return
		}
		budget--
		if budget < 0 {
			return // anti-runaway guard (never hit by a converging network)
		}
		state := t.redstoneBlockAt(pos)
		switch {
		case block.IsRedstoneWire(state):
			// RedStoneWireBlock.neighborChanged: if canSurvive, updatePowerStrength; else drop+remove.
			if t.wireCanSurvive(pos) {
				t.updateWirePowerStrength(pos, state, q)
			} else {
				t.dropAndRemoveWire(pos, state)
			}
		case block.IsRedstoneTorch(state) || block.IsRedstoneWallTorch(state):
			t.torchNeighborChanged(pos, state)
		case block.IsRepeater(state) || block.IsComparator(state):
			// DiodeBlock.neighborChanged -> checkTickOnNeighbor: schedule the delayed output flip if the
			// diode's input state changed (REDSTONE TIER-2, redstone_diode.go). CITE: DiodeBlock.neighborChanged.
			t.diodeNeighborChanged(pos, state)
		case block.IsPiston(state):
			// PistonBaseBlock.neighborChanged -> checkIfExtend: post an extend/retract block event if the
			// piston's powered state crossed its EXTENDED state (REDSTONE TIER-3, piston.go). CITE:
			// PistonBaseBlock.neighborChanged.
			t.pistonCheckIfExtend(pos, state)
		case block.IsDispenserFamily(state):
			// DispenserBlock.neighborChanged (shared by DropperBlock): on a rising power edge (now powered,
			// not yet TRIGGERED) schedule the dispense 4 ticks out + latch TRIGGERED=true; on a falling edge
			// clear TRIGGERED (REDSTONE TIER-4, dispenser.go). CITE: DispenserBlock.neighborChanged.
			t.dispenserNeighborChanged(pos, state)
			// lever / button / redstone_block / observer have no redstone neighborChanged reaction here
			// (observer reacts to updateShape via onObserverEdit, not neighborChanged).
		case block.IsHopper(state):
			// HopperBlock.neighborChanged -> checkPoweredState: a hopper is LOCKED (ENABLED=false) while any
			// neighbor emits signal, unlocked otherwise (hopper_be.go). CITE: HopperBlock.neighborChanged.
			t.hopperNeighborChanged(pos, state)
		case isTntBlock(state):
			// TntBlock.neighborChanged (shared logic with onPlace): if the block now has a neighbor signal,
			// prime the TNT (spawn a PrimedTnt) and removeBlock. This fires on a redstone rising edge that
			// reaches the TNT (a lever/button/wire/torch powering an adjacent cell). CITE: TntBlock
			// .neighborChanged: `if (hasNeighborSignal(pos) && TntBlock.prime(level, pos)) removeBlock(pos, false)`.
			if t.hasNeighborSignal(pos) && t.primeTntBlock(pos) {
				air := t.airState()
				if t.world().SetBlock(pos, air, dimMinY) {
					t.broadcastBlockUpdate(pos, air)
				}
			}
		}
	}
}

// wireCanSurvive is RedStoneWireBlock.canSurvive: the block directly below can host a wire
// (canSurviveOn). CITE: RedStoneWireBlock.canSurvive.
func (t *TickLoop) wireCanSurvive(pos pk.Position) bool {
	below := relative(pos, block.Down)
	return t.wireCanSurviveOn(below, t.redstoneBlockAt(below))
}

// dropAndRemoveWire is RedStoneWireBlock.neighborChanged's else branch: dropResources + removeBlock.
// CITE: RedStoneWireBlock.neighborChanged (`dropResources(state,level,pos); level.removeBlock(pos,false)`).
func (t *TickLoop) dropAndRemoveWire(pos pk.Position, state block.StateID) {
	if !t.world().SetBlock(pos, t.airState(), dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, t.airState())
	t.spawnBlockDrop(nil, pos, state)
	// removeBlock -> updateNeighborsAt: re-run the redstone edit hook so neighbors recompute without
	// this wire. Nested via a fresh queue keeps the semantics (the cell is now air).
	t.onRedstoneEdit(pos)
}

// ---------------------------------------------------------------------------------------------
// Redstone torch — scheduled-tick burnout (RedstoneTorchBlock)
// ---------------------------------------------------------------------------------------------

// torchAttachmentHasSignal is RedstoneTorchBlock/RedstoneWallTorchBlock.hasNeighborSignal(level,
// pos, state): the standing torch reads its attachment (the block BELOW) via hasSignal(below, DOWN);
// the wall torch reads the block it is mounted on (pos.relative(FACING.opposite)) via
// hasSignal(that, FACING.opposite). CITE: RedstoneTorchBlock.hasNeighborSignal /
// RedstoneWallTorchBlock.hasNeighborSignal.
func (t *TickLoop) torchAttachmentHasSignal(pos pk.Position, state block.StateID) bool {
	if block.IsRedstoneWallTorch(state) {
		facing, ok := block.RedstoneWallTorchFacing(state)
		if !ok {
			return false
		}
		opp := dirOpposite(facing)
		return t.hasSignal(relative(pos, opp), opp)
	}
	// standing torch: attachment is the block below.
	return t.hasSignal(relative(pos, block.Down), block.Down)
}

// torchNeighborChanged is RedstoneTorchBlock.neighborChanged: if the torch's LIT no longer matches
// the (inverted) presence of its attachment signal — i.e. LIT == hasNeighborSignal, meaning the
// torch SHOULD flip — and it is not already ticking this tick, schedule the 2-tick toggle. CITE:
// RedstoneTorchBlock.neighborChanged (`if (LIT == hasNeighborSignal && !willTickThisTick)
// scheduleTick(pos, this, 2)`).
func (t *TickLoop) torchNeighborChanged(pos pk.Position, state block.StateID) {
	lit := block.RedstoneTorchLit(state)
	if lit != t.torchAttachmentHasSignal(pos, state) {
		return // LIT already correct (lit==!signal): no flip needed.
	}
	typ := blockTickType(block.StateList[state].ID())
	if t.willTickThisTick(pos, typ) {
		return
	}
	t.scheduleBlockTick(pos, typ, redstoneTorchToggleDelay)
}

// redstoneTorchToggleDelay is RedstoneTorchBlock.TOGGLE_DELAY (2). CITE: RedstoneTorchBlock.TOGGLE_DELAY.
const redstoneTorchToggleDelay = 2

// redstoneTorchRecentToggleTimer is RECENT_TOGGLE_TIMER (60 game ticks) — the sliding window over
// which recent toggles at a position are counted for burnout. CITE: RedstoneTorchBlock.RECENT_TOGGLE_TIMER.
const redstoneTorchRecentToggleTimer = 60

// redstoneTorchMaxRecentToggles is MAX_RECENT_TOGGLES (8) — a torch that toggles >= 8 times within
// the 60-tick window "burns out" and reschedules its next attempt RESTART_DELAY ticks out. CITE:
// RedstoneTorchBlock.MAX_RECENT_TOGGLES.
const redstoneTorchMaxRecentToggles = 8

// redstoneTorchRestartDelay is RESTART_DELAY (160) — the cooldown a burned-out torch waits before
// its next toggle attempt. CITE: RedstoneTorchBlock.RESTART_DELAY.
const redstoneTorchRestartDelay = 160

// redstoneTorchTick is RedstoneTorchBlock.tick(state, level, pos, random):
//
//	boolean sig = hasNeighborSignal(level, pos, state);
//	prune RECENT_TOGGLES older than 60 ticks (gameTime - when > 60);
//	if (LIT) {
//	    if (sig) {
//	        level.setBlock(pos, state.setValue(LIT, false), 3);
//	        if (isToggledTooFrequently(level, pos, true)) { levelEvent(1502,...); scheduleTick(pos, block, 160); }
//	    }
//	} else if (!sig && !isToggledTooFrequently(level, pos, false)) {
//	    level.setBlock(pos, state.setValue(LIT, true), 3);
//	}
//
// setBlock flag 3 == UPDATE_NEIGHBORS|UPDATE_CLIENTS -> we mirror with SetBlock + broadcast +
// onRedstoneEdit (the neighbor-update the flag's bit-1 performs). CITE: RedstoneTorchBlock.tick.
func (t *TickLoop) redstoneTorchTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	sig := t.torchAttachmentHasSignal(pos, state)
	t.pruneRecentToggles()
	lit := block.RedstoneTorchLit(state)
	if lit {
		if sig {
			// unlight.
			if newState, ok := block.RedstoneTorchWithLit(state, false); ok && t.world().SetBlock(pos, newState, dimMinY) {
				t.broadcastBlockUpdate(pos, newState)
				t.onRedstoneEdit(pos)
			}
			if t.isToggledTooFrequently(pos, true) {
				// levelEvent(1502) is the burnout smoke/sound — a client effect, DEFERRED (no gameplay
				// impact). The reschedule is gameplay and IS ported.
				typ := blockTickType(block.StateList[t.redstoneBlockAt(pos)].ID())
				t.scheduleBlockTick(pos, typ, redstoneTorchRestartDelay)
			}
		}
	} else if !sig && !t.isToggledTooFrequently(pos, false) {
		// relight.
		if newState, ok := block.RedstoneTorchWithLit(state, true); ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.onRedstoneEdit(pos)
		}
	}
}

// redstoneToggle is one RECENT_TOGGLES entry: a (pos, gameTime) pair. CITE: RedstoneTorchBlock.Toggle.
type redstoneToggle struct {
	pos  pk.Position
	when int64
}

// pruneRecentToggles drops toggles older than the 60-tick window from the FRONT of the list, exactly
// as tick() does: `while (!toggles.isEmpty() && gameTime - toggles.get(0).when > 60) toggles.remove(0)`.
// The list is stored per-level on the region so it is tick-owned. CITE: RedstoneTorchBlock.tick prune loop.
func (t *TickLoop) pruneRecentToggles() {
	toggles := t.cur().redstoneToggles
	i := 0
	for i < len(toggles) && t.gametime-toggles[i].when > redstoneTorchRecentToggleTimer {
		i++
	}
	if i > 0 {
		t.cur().redstoneToggles = toggles[i:]
	}
}

// isToggledTooFrequently is RedstoneTorchBlock.isToggledTooFrequently(level, pos, add): optionally
// append a fresh toggle for pos (add==true, done on the LIT->unlit transition), then count entries
// at pos; return true once the count reaches MAX_RECENT_TOGGLES (8). CITE:
// RedstoneTorchBlock.isToggledTooFrequently.
func (t *TickLoop) isToggledTooFrequently(pos pk.Position, add bool) bool {
	if add {
		t.cur().redstoneToggles = append(t.cur().redstoneToggles, redstoneToggle{pos: pos, when: t.gametime})
	}
	count := 0
	for _, tg := range t.cur().redstoneToggles {
		if tg.pos == pos {
			count++
			if count >= redstoneTorchMaxRecentToggles {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------
// Lever + button — interaction toggle + scheduled unpress
// ---------------------------------------------------------------------------------------------

// useLever is LeverBlock.useWithoutItem -> pull(state, level, pos, player): cycle POWERED, setBlock
// flag 3, updateNeighbours. Returns true (the interaction is consumed, so placement is skipped).
// CITE: LeverBlock.useWithoutItem / pull.
func (t *TickLoop) useLever(pos pk.Position, state block.StateID) bool {
	newState, ok := block.LeverToggled(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true // consumed the click even if the write no-oped (matches InteractionResult.SUCCESS)
	}
	t.broadcastBlockUpdate(pos, newState)
	t.leverUpdateNeighbours(newState, pos)
	return true
}

// leverUpdateNeighbours is LeverBlock.updateNeighbours(state, level, pos):
// updateNeighborsAt(pos) and updateNeighborsAt(pos.relative(getConnectedDirection().opposite)).
// The lever pushes updates at its own cell and at the cell it is attached to (behind it). CITE:
// LeverBlock.updateNeighbours.
func (t *TickLoop) leverUpdateNeighbours(state block.StateID, pos pk.Position) {
	t.onRedstoneEdit(pos)
	if cd, ok := block.LeverConnectedDirection(state); ok {
		front := dirOpposite(cd)
		t.onRedstoneEdit(relative(pos, front))
	}
}

// pressButton is ButtonBlock.useWithoutItem -> press: if already POWERED, CONSUME (no re-press);
// else set POWERED=true (flag 3), updateNeighbours, scheduleTick(ticksToStayPressed). Returns true
// (interaction consumed, placement skipped). CITE: ButtonBlock.useWithoutItem / press.
func (t *TickLoop) pressButton(pos pk.Position, state block.StateID) bool {
	if block.ButtonPowered(state) {
		return true // InteractionResult.CONSUME: already pressed, nothing happens.
	}
	newState, ok := block.ButtonWithPowered(state, true)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true
	}
	t.broadcastBlockUpdate(pos, newState)
	t.buttonUpdateNeighbours(newState, pos)
	// scheduleTick(pos, this, ticksToStayPressed) — the unpress. 20 for stone/polished_blackstone,
	// 30 for wooden/bamboo/nether (jar-verified).
	delay := block.ButtonStayPressedTicks(newState)
	typ := blockTickType(block.StateList[newState].ID())
	t.scheduleBlockTick(pos, typ, delay)
	return true
}

// buttonUpdateNeighbours is ButtonBlock.updateNeighbours (same shape as the lever's): update pos and
// pos.relative(getConnectedDirection().opposite). CITE: ButtonBlock.updateNeighbours.
func (t *TickLoop) buttonUpdateNeighbours(state block.StateID, pos pk.Position) {
	t.onRedstoneEdit(pos)
	if cd, ok := block.ButtonConnectedDirection(state); ok {
		front := dirOpposite(cd)
		t.onRedstoneEdit(relative(pos, front))
	}
}

// buttonTick is ButtonBlock.tick -> checkPressed: for a NON-arrow button (the only kind here, since
// arrow activation is DEFERRED — no arrow entity redstone), shouldBePressed is false, so a POWERED
// button unpresses (POWERED=false, flag 3, updateNeighbours). An already-unpowered button does
// nothing. CITE: ButtonBlock.tick / checkPressed (arrow branch DEFERRED: canButtonBeActivatedByArrows
// is only true for wooden buttons + arrows, and no arrow entities feed redstone yet).
func (t *TickLoop) buttonTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if !block.ButtonPowered(state) {
		return // tick early-returns when not POWERED.
	}
	// checkPressed: firstArrow == null -> shouldBePressed=false != wasPressed(true) -> unpress.
	newState, ok := block.ButtonWithPowered(state, false)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, newState)
	t.buttonUpdateNeighbours(newState, pos)
	// shouldBePressed is false, so checkPressed does NOT reschedule another tick.
}
