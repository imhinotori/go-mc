package server

// redstone_blocks.go - the server half of the NoteBlock / TargetBlock / DaylightDetector /
// TripWireHook + TripWire reactions: the runtime neighborChanged / scheduled-tick / signal-source
// behavior, ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via
// javap -c -p this session. The block-state shape lives in level/block/redstone_blocks.go; this file
// is the SERVER half wired into the updateShape/neighborChanged dispatch + scheduled-block-tick subsystem.
//
// CITE (jar-verified this session):
//   NoteBlock.neighborChanged/playNote
//   TargetBlock.tick/ownSignal/isSignalSource/getRedstoneStrength/setOutputPower/updateRedstoneOutput
//   DaylightDetectorBlock.updateSignalStrength/tickEntity/ownSignal/isSignalSource
//   TripWireHookBlock.calculateState/emitState/notifyNeighbors/tick/ownSignal/getDirectSignal/isSignalSource
//   TripWireBlock.updateSource/checkPressed/entityInside/tick/shouldConnectTo

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// levelEventNotePlay is the blockEvent id NoteBlock.playNote posts (blockEvent(pos, this, 0, 0)). The
// audible note is a pure client effect so the broadcast is a cited no-op seam; the POWERED flip and the
// rising-edge gate ARE gameplay and are ported. CITE: NoteBlock.playNote.
const levelEventNotePlay = 0

// noteBlockNeighborChanged is NoteBlock.neighborChanged: hasSignal = level.hasNeighborSignal(pos); if it
// differs from POWERED, play the note on a rising edge and write the new POWERED (setBlock flag 3).
// CITE: NoteBlock.neighborChanged.
func (t *TickLoop) noteBlockNeighborChanged(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	hasSignal := t.hasNeighborSignal(pos)
	if hasSignal == block.NoteBlockPowered(state) {
		return
	}
	if hasSignal {
		t.noteBlockPlayNote(pos, state)
	}
	if newState, ok := block.NoteBlockWithPowered(state, hasSignal); ok {
		if t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.onRedstoneEdit(pos)
		}
	}
}

// noteBlockPlayNote is NoteBlock.playNote: play only if the instrument worksAboveNoteBlock OR the cell
// above is air. The blockEvent(0,0) is a cited client no-op; the NOTE_BLOCK_PLAY gameEvent is the
// load-bearing vibration (frequency 10). The gate is ported so a covered note block stays silent. CITE:
// NoteBlock.playNote (level.blockEvent + level.gameEvent(entity, NOTE_BLOCK_PLAY, pos)).
func (t *TickLoop) noteBlockPlayNote(pos pk.Position, state block.StateID) {
	inst, ok := block.NoteBlockInstrumentOf(state)
	if !ok {
		return
	}
	if !block.NoteInstrumentWorksAboveNoteBlock(inst) {
		aboveState, ok := t.world().GetBlock(above(pos), dimMinY)
		if !ok || !block.IsAir(aboveState) {
			return
		}
	}
	t.recordNotePlayed(pos)
	// level.gameEvent(entity, GameEvent.NOTE_BLOCK_PLAY, pos): the redstone-driven play has no entity
	// source (a hand-strike would pass the player; redstone edge is source 0).
	t.gameEventAt(geNoteBlockPlay, pos, gameEventContext{})
}

// recordNotePlayed records that a note block at pos played this tick (the audible blockEvent is a
// deferred client effect). Lets a test assert the rising-edge play. CITE: NoteBlock.playNote.
func (t *TickLoop) recordNotePlayed(pos pk.Position) {
	r := t.cur()
	if r == nil {
		return
	}
	r.notesPlayed = append(r.notesPlayed, pos)
}

// targetActivationTicksArrows is TargetBlock.ACTIVATION_TICKS_ARROWS (20). CITE: TargetBlock.updateRedstoneOutput.
const targetActivationTicksArrows = 20

// targetActivationTicksOther is TargetBlock.ACTIVATION_TICKS_OTHER (8). CITE: TargetBlock.updateRedstoneOutput.
const targetActivationTicksOther = 8

// targetGetRedstoneStrength is TargetBlock.getRedstoneStrength: d = per-axis max of abs(frac(loc)-0.5);
// return max(1, ceil(15 * clamp((0.5-d)/0.5, 0, 1))). Center hit -> 15, rim hit -> 1. CITE: TargetBlock.getRedstoneStrength.
func targetGetRedstoneStrength(hitDir block.Direction, locX, locY, locZ float64) int {
	dx := math.Abs(mthFracD(locX) - 0.5)
	dy := math.Abs(mthFracD(locY) - 0.5)
	dz := math.Abs(mthFracD(locZ) - 0.5)
	var d float64
	switch axisOf(hitDir) {
	case axisY:
		d = math.Max(dx, dz)
	case axisZ:
		d = math.Max(dx, dy)
	default:
		d = math.Max(dy, dz)
	}
	// Mth.clamp is applied to a float; the existing mthClampF is the float32 form, matching vanilla
	// (getRedstoneStrength clamps a float). CITE: TargetBlock.getRedstoneStrength.
	scaled := 15.0 * float64(mthClampF(float32((0.5-d)/0.5), 0.0, 1.0))
	strength := int(math.Ceil(scaled))
	if strength < 1 {
		return 1
	}
	return strength
}

// targetSetOutputPower is TargetBlock.setOutputPower: setBlock(OUTPUT_POWER=power, 3) then
// scheduleTick(pos, this, activationTicks). CITE: TargetBlock.setOutputPower.
func (t *TickLoop) targetSetOutputPower(pos pk.Position, state block.StateID, power, activationTicks int) {
	if t.world() == nil {
		return
	}
	newState, ok := block.TargetWithOutputPower(state, power)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
		t.onRedstoneEdit(pos)
	}
	t.scheduleBlockTick(pos, targetTickType, activationTicks)
}

// targetProjectileHit folds TargetBlock.updateRedstoneOutput with onProjectileHit: compute the strength,
// and if no reset tick is pending set OUTPUT_POWER + schedule the reset (20 arrow / 8 other). Returns the
// strength. Hook for a future arrow/projectile path; the state math is fully ported now. CITE:
// TargetBlock.updateRedstoneOutput / onProjectileHit.
func (t *TickLoop) targetProjectileHit(pos pk.Position, state block.StateID, hitDir block.Direction, locX, locY, locZ float64, isArrow bool) int {
	strength := targetGetRedstoneStrength(hitDir, locX, locY, locZ)
	activation := targetActivationTicksOther
	if isArrow {
		activation = targetActivationTicksArrows
	}
	if !t.hasScheduledBlockTick(pos, targetTickType) {
		t.targetSetOutputPower(pos, state, strength, activation)
	}
	return strength
}

// targetTick is TargetBlock.tick: if OUTPUT_POWER != 0, reset it to 0 (setBlock flag 3). CITE: TargetBlock.tick.
func (t *TickLoop) targetTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if block.TargetOutputPower(state) == 0 {
		return
	}
	if off, ok := block.TargetWithOutputPower(state, 0); ok {
		if t.world().SetBlock(pos, off, dimMinY) {
			t.broadcastBlockUpdate(pos, off)
			t.onRedstoneEdit(pos)
		}
	}
}

// daylightSunAngleRadians is 0.017453292f (deg->rad). CITE: DaylightDetectorBlock.updateSignalStrength.
const daylightSunAngleRadians = 0.017453292

// daylightSunAngleDeg is the SUN_ANGLE env attribute in DEGREES. The day/night clock is not built in v1,
// so this is a CITED CONSTANT equal to the vanilla DAY default (0 deg, cos term == 1 so POWER ==
// skyBrightness). Structured to become a real EnvironmentAttributes.SUN_ANGLE read later - never baked
// away. CITE: DaylightDetectorBlock.updateSignalStrength (EnvironmentAttributes.SUN_ANGLE).
const daylightSunAngleDeg = 0.0

// daylightUpdateSignalStrength is DaylightDetectorBlock.updateSignalStrength: sky =
// getEffectiveSkyBrightness(pos); if INVERTED sky = 15 - sky; else if sky > 0 apply the sun-angle cos
// scaling; clamp(0,15); write POWER on change (setBlock 3). getEffectiveSkyBrightness == getBrightness(
// SKY, pos) - getSkyDarken(). CITE: DaylightDetectorBlock.updateSignalStrength.
func (t *TickLoop) daylightUpdateSignalStrength(pos pk.Position, state block.StateID) {
	w := t.world()
	if w == nil {
		return
	}
	sky := w.SkyBrightness(pos, dimMinY) - skyDarkenDay
	sunAngle := float32(daylightSunAngleDeg) * float32(daylightSunAngleRadians)
	if block.DaylightInverted(state) {
		sky = 15 - sky
	} else if sky > 0 {
		var twoPiOrZero float32
		if sunAngle < math.Pi {
			twoPiOrZero = 0.0
		} else {
			twoPiOrZero = 2 * math.Pi
		}
		sunAngle += (twoPiOrZero - sunAngle) * 0.2
		sky = int(math.Round(float64(float32(sky) * float32(math.Cos(float64(sunAngle))))))
	}
	sky = int(mthClampI(int32(sky), 0, 15))
	if block.DaylightPower(state) == sky {
		return
	}
	if newState, ok := block.DaylightWithPower(state, sky); ok {
		if w.SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.onRedstoneEdit(pos)
		}
	}
}

// daylightTick runs updateSignalStrength then reschedules onto the next game-time multiple of 20,
// matching DaylightDetectorBlockEntity.tickEntity (gameTime % 20 == 0). CITE: DaylightDetectorBlock.tickEntity.
func (t *TickLoop) daylightTick(state block.StateID, pos pk.Position) {
	t.daylightUpdateSignalStrength(pos, state)
	t.scheduleDaylightTick(pos)
}

// scheduleDaylightTick schedules the next detector tick onto the next game-time multiple of 20 (the
// tickEntity gate). Idempotent via hasScheduledTick. CITE: DaylightDetectorBlock.tickEntity boundary.
func (t *TickLoop) scheduleDaylightTick(pos pk.Position) {
	if t.hasScheduledBlockTick(pos, daylightDetectorTickType) {
		return
	}
	delay := 20 - int(t.gametime%20)
	if delay <= 0 {
		delay = 20
	}
	t.scheduleBlockTick(pos, daylightDetectorTickType, delay)
}

const tripwireWireDistMax = 42
const tripwireRecheckPeriod = 10

// tripwireHookCalculateState is TripWireHookBlock.calculateState. Walk up to 42 cells in FACING, record
// every TripWire, stop at the opposing hook; decide the hook + span ATTACHED/POWERED; write the hook, the
// opposing hook, and the in-between wires. CITE: TripWireHookBlock.calculateState.
func (t *TickLoop) tripwireHookCalculateState(pos pk.Position, hookState block.StateID, attaching bool, wireIndex int, wireOverride block.StateID, hasOverride bool) {
	w := t.world()
	if w == nil {
		return
	}
	facing, ok := block.TripwireHookFacing(hookState)
	if !ok {
		return // getOptionalValue(FACING) empty: not a hook.
	}
	oldAttached := block.TripwireHookAttached(hookState)
	oldPowered := block.TripwireHookPowered(hookState)

	// Vanilla locals: bl (spanAttached accumulator, seeded !attaching), bl2 (spanPowered accumulator,
	// seeded false), k (completeAt, seeded 0), plus the wireStates array. CITE: TripWireHookBlock
	// .calculateState (istore 12 = !iload_3; istore 13 = 0; istore 14 = 0).
	spanAttached := !attaching
	spanPowered := false
	completeAt := 0
	haveWire := make([]bool, tripwireWireDistMax)

	for i := 1; i < tripwireWireDistMax; i++ {
		np := relativeN(pos, facing, i)
		ns, okRead := w.GetBlock(np, dimMinY)
		if !okRead {
			ns = t.airState()
		}
		if block.IsTripwireHook(ns) {
			// A facing opposing hook (FACING == facing.opposite) completes the circuit at k=i.
			if hf, ok := block.TripwireHookFacing(ns); ok && hf == dirOpposite(facing) {
				completeAt = i
			}
			break
		}
		if block.IsTripwire(ns) || i == wireIndex {
			if i == wireIndex && hasOverride {
				// firstNonNull(wireOverride, ns): use the substituted state at the changed index.
				ns = wireOverride
			}
			notDisarmed := !block.TripwireDisarmed(ns) // bl4 == !DISARMED
			wirePowered := block.TripwirePowered(ns)   // bl5 == POWERED
			spanPowered = spanPowered || (notDisarmed && wirePowered)
			haveWire[i] = true
			if i == wireIndex {
				// scheduleTick is posted at the HOOK pos (aload_1), and spanAttached is ANDed with bl4
				// ONLY at the changed wire index. CITE: TripWireHookBlock.calculateState (offsets 275-289).
				t.scheduleBlockTick(pos, tripwireTickType, tripwireRecheckPeriod)
				spanAttached = spanAttached && notDisarmed
			}
		} else {
			// A gap (non-wire, non-hook): the span is broken (bl = false). CITE: offset 300 iconst_0.
			haveWire[i] = false
			spanAttached = false
		}
	}

	// bl = bl && (k > 1); bl2 = bl2 && bl. CITE: offsets 309-330.
	spanAttached = spanAttached && completeAt > 1
	spanPowered = spanPowered && spanAttached

	// defaultBlockState() is FACING=NORTH (a valid horizontal hook state); TripwireHookWith then sets
	// the real facing. A bare TripwireHook{} (Facing=Down) is not a valid blockstate. CITE:
	// TripWireHookBlock constructor registerDefaultState(FACING=NORTH).
	hookDefault := block.ToStateID[block.TripwireHook{Facing: block.North}]
	newHook, _ := block.TripwireHookWith(hookDefault, spanAttached, spanPowered, facing)

	if completeAt > 0 {
		// Write the opposing hook (FACING == facing.opposite), notify its neighbors, emit its state.
		oppPos := relativeN(pos, facing, completeAt)
		oppFacing := dirOpposite(facing)
		oppHook, _ := block.TripwireHookWith(hookDefault, spanAttached, spanPowered, oppFacing)
		if w.SetBlock(oppPos, oppHook, dimMinY) {
			t.broadcastBlockUpdate(oppPos, oppHook)
		}
		t.tripwireNotifyNeighbors(oppPos, oppFacing)
		// If pos is still a hook, emit the opposing-hook state transition. CITE: calculateState
		// (getBlockState(pos).is(TRIPWIRE_HOOK) -> emitState(oppPos, ...)).
		if cur, okc := w.GetBlock(pos, dimMinY); okc && block.IsTripwireHook(cur) {
			t.tripwireEmitState(oppPos, spanAttached, spanPowered, oldAttached, oldPowered)
		}
	}

	// emitState at pos, then (when NOT attaching) write pos hook + notify. CITE: calculateState
	// (emitState(pos,...); if (!bl12==attaching?) setBlock(pos, hook, 3); if (bl) notifyNeighbors).
	t.tripwireEmitState(pos, spanAttached, spanPowered, oldAttached, oldPowered)
	if !attaching {
		if w.SetBlock(pos, newHook, dimMinY) {
			t.broadcastBlockUpdate(pos, newHook)
		}
		// notifyNeighbors is gated on the vanilla `bl4` (the flag param bl2 == param4). setPlacedBy/onPlace
		// pass it false; tick/updateSource pass it true. We model that via `!attaching` here matching the
		// two live callers (tick + updateSource notify; setPlacedBy attaching=false does not double-notify).
		t.tripwireNotifyNeighbors(pos, facing)
	}

	// If the span ATTACHED flag changed, propagate the new ATTACHED down every wire in the span. CITE:
	// calculateState tail (bl9 != bl -> for each in-between wire trySetValue(ATTACHED, bl)).
	if oldAttached != spanAttached {
		for i := 1; i < completeAt; i++ {
			if !haveWire[i] {
				continue
			}
			wp := relativeN(pos, facing, i)
			ws, okw := w.GetBlock(wp, dimMinY)
			if !okw {
				continue
			}
			if block.IsTripwire(ws) {
				if nw, ok := block.TripwireWithAttached(ws, spanAttached); ok {
					if w.SetBlock(wp, nw, dimMinY) {
						t.broadcastBlockUpdate(wp, nw)
					}
				}
			}
		}
	}
}

// tripwireEmitState is TripWireHookBlock.emitState: CLICK_ON/CLICK_OFF/ATTACH/DETACH sound+vibration.
// Pure client effects with no server gameplay -> cited no-op seam. CITE: TripWireHookBlock.emitState.
func (t *TickLoop) tripwireEmitState(_ pk.Position, _ bool, _ bool, _ bool, _ bool) {
}

// tripwireNotifyNeighbors is TripWireHookBlock.notifyNeighbors: updateNeighborsAt(pos) and
// updateNeighborsAt(pos.relative(facing.opposite)). CITE: TripWireHookBlock.notifyNeighbors.
func (t *TickLoop) tripwireNotifyNeighbors(pos pk.Position, facing block.Direction) {
	t.onRedstoneEdit(pos)
	t.onRedstoneEdit(relative(pos, dirOpposite(facing)))
}

// tripwireUpdateSource is TripWireBlock.updateSource: for each of SOUTH and WEST walk outward up to 42
// cells; at the first facing hook run the hook calculateState with this wire as the changed index; stop
// at the first non-wire that is not this wire block. CITE: TripWireBlock.updateSource.
func (t *TickLoop) tripwireUpdateSource(pos pk.Position, wireState block.StateID) {
	w := t.world()
	if w == nil {
		return
	}
	for _, dir := range [2]block.Direction{block.South, block.West} {
		for i := 1; i < tripwireWireDistMax; i++ {
			np := relativeN(pos, dir, i)
			ns, okRead := w.GetBlock(np, dimMinY)
			if !okRead {
				break
			}
			if block.IsTripwireHook(ns) {
				if hf, ok := block.TripwireHookFacing(ns); ok && hf == dirOpposite(dir) {
					t.tripwireHookCalculateState(np, ns, false, i, wireState, true)
				}
				break
			}
			if !block.IsTripwire(ns) {
				break
			}
		}
	}
}

// tripwireCheckPressed is TripWireBlock.checkPressed: nowPowered = any entity in the box not ignoring
// block triggers; if it differs from POWERED, setBlock(POWERED, 3) + updateSource; then schedule the
// recheck (10 while pressed, 0 on release). CITE: TripWireBlock.checkPressed.
func (t *TickLoop) tripwireCheckPressed(pos pk.Position, entitiesPresent bool) {
	w := t.world()
	if w == nil {
		return
	}
	state, ok := w.GetBlock(pos, dimMinY)
	if !ok || !block.IsTripwire(state) {
		return
	}
	wasPowered := block.TripwirePowered(state)
	nowPowered := entitiesPresent
	if nowPowered != wasPowered {
		if ns, ok := block.TripwireWithPowered(state, nowPowered); ok {
			if w.SetBlock(pos, ns, dimMinY) {
				t.broadcastBlockUpdate(pos, ns)
			}
			state = ns
			t.tripwireUpdateSource(pos, state)
		}
	}
	if nowPowered {
		t.scheduleBlockTick(pos, tripwireTickType, tripwireRecheckPeriod)
	} else if wasPowered {
		t.scheduleBlockTick(pos, tripwireTickType, 0)
	}
}

// tripwireTick is TripWireBlock.tick: if POWERED, re-check the pressed state. The entity broad-phase for
// tripwire pressure is not wired in v1 (no entityInside dispatch), so the re-check probes the current
// entity presence (empty in v1), matching vanilla when the box is empty (a POWERED wire with nothing on
// it unpresses). CITE: TripWireBlock.tick.
func (t *TickLoop) tripwireTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if !block.TripwirePowered(state) {
		return
	}
	t.tripwireCheckPressed(pos, t.tripwireEntitiesPresent(pos))
}

// tripwireEntitiesPresent probes whether any non-block-trigger-ignoring entity overlaps the wire cell.
// Not wired in v1 -> returns false (empty box). A future entityInside wiring replaces this with the real
// getEntities(box) scan. CITE: TripWireBlock.checkPressed (empty list -> false).
func (t *TickLoop) tripwireEntitiesPresent(_ pk.Position) bool {
	return false
}

// blockAxis identifies the Direction.getAxis() result for the getRedstoneStrength branch.
type blockAxis int

const (
	axisX blockAxis = iota
	axisY
	axisZ
)

// axisOf is Direction.getAxis(). CITE: Direction.getAxis.
func axisOf(d block.Direction) blockAxis {
	switch d {
	case block.Down, block.Up:
		return axisY
	case block.North, block.South:
		return axisZ
	default:
		return axisX
	}
}
