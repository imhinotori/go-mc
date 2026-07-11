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

	"github.com/imhinotori/sulfur/data/entity"
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

// daylightSunAngleDeg is the SUN_ANGLE environment attribute in DEGREES for the current game time -
// the input DaylightDetectorBlock.updateSignalStrength multiplies by 0.017453292 (deg->rad). In 26.2
// SUN_ANGLE is the data-driven Timelines.OVERWORLD_DAY keyframe track (visual/sun_angle: 360 deg at
// tick 0 wrapping to 0 at tick 6000 - noon - via the smooth easing). Its observable curve is the
// historical Level.getTimeOfDay/getSunAngle formula, so this ports that (cited) formula, which
// reproduces the OVERWORLD_DAY track's output 1:1:
//
//	d = frac(dayTime / 24000.0 - 0.25);        // Mth.frac; dayTime = gameTime % 24000
//	e = 0.5 - cos(d * PI) / 2.0;               // smoothstep toward the solstices
//	timeOfDay = (d * 2.0 + e) / 3.0;           // getTimeOfDay(1.0F)
//	sunAngleDeg = timeOfDay * 360.0;           // SUN_ANGLE == getSunAngle in degrees
//
// At noon (dayTime 6000) d=0, e=0, timeOfDay=0 -> 0 deg (cos term 1 -> POWER == skyBrightness); at
// midnight (dayTime 18000) timeOfDay=0.5 -> 180 deg. Feeds off the single gametime counter time.go
// derives dayTime from; becomes a direct EnvironmentAttributes.SUN_ANGLE read when the keyframe
// timeline subsystem lands. CITE: DaylightDetectorBlock.updateSignalStrength
// (EnvironmentAttributes.SUN_ANGLE); Timelines.OVERWORLD_DAY visual/sun_angle track; the legacy
// Level.getTimeOfDay/getSunAngle formula it reproduces.
func (t *TickLoop) daylightSunAngleDeg() float64 {
	dayTime := float64(t.gametime % 24000)
	d := mthFracD(dayTime/24000.0 - 0.25)
	e := 0.5 - math.Cos(d*math.Pi)/2.0
	timeOfDay := (d*2.0 + e) / 3.0
	return timeOfDay * 360.0
}

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
	sunAngle := float32(t.daylightSunAngleDeg()) * float32(daylightSunAngleRadians)
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

// tripwireTick is TripWireBlock.tick: if POWERED, re-check the pressed state. The re-check probes the
// current entity presence via tripwireEntitiesPresent (the checkPressed(Level,BlockPos) getEntities scan);
// with an occupant still on the wire it stays POWERED and reschedules, with the box now empty it unpresses.
// CITE: TripWireBlock.tick (-> checkPressed(Level, BlockPos)).
func (t *TickLoop) tripwireTick(state block.StateID, pos pk.Position) {
	if t.world() == nil {
		return
	}
	if !block.TripwirePowered(state) {
		return
	}
	t.tripwireCheckPressed(pos, t.tripwireEntitiesPresent(pos))
}

// tripwireShape*MinY/MaxY are the Y bounds (in blocks) of the tripwire collision shape bounds, keyed on
// ATTACHED. Both SHAPE_ATTACHED and SHAPE_NOT_ATTACHED span the full XZ footprint (Block.column(16,..)
// -> box 0..16 in sixteenths -> 0..1 block) and start at their respective minY; checkPressed uses
// state.getShape(...).bounds().move(pos) as the scan box, so only the Y extent varies:
//
//	SHAPE_ATTACHED     = Block.column(16, 1, 2.5) -> bounds (0, 1/16, 0)..(1, 2.5/16, 1)
//	SHAPE_NOT_ATTACHED = Block.column(16, 0, 8)   -> bounds (0, 0,    0)..(1, 8/16,  1)
//
// CITE: TripWireBlock static {} (SHAPE_ATTACHED = column(16d,1d,2.5d); SHAPE_NOT_ATTACHED = column(16d,
// 0d,8d)); Block.column(w,minY,maxY) -> box(8-w/2, minY, 8-w/2, 8+w/2, maxY, 8+w/2) in sixteenths.
const (
	tripwireShapeAttachedMinY    = 1.0 / 16.0
	tripwireShapeAttachedMaxY    = 2.5 / 16.0
	tripwireShapeNotAttachedMinY = 0.0
	tripwireShapeNotAttachedMaxY = 8.0 / 16.0
)

// tripwireEntitiesPresent is the entity-scan half of TripWireBlock.checkPressed(Level, BlockPos): build the
// wire shape AABB moved to pos (state.getShape(level, pos).bounds().move(pos)), collect every entity whose
// bounding box intersects it (level.getEntities(null, box)), and report whether ANY of them is not
// isIgnoringBlockTriggers() -- i.e. the three-arg checkPressed's `bl2 = any e where !e.isIgnoringBlock
// Triggers()`. Reuses the pressure-plate entity-in-box scan convention (server/pressure_plate.go
// plateEntityCount): the same t.players + t.entitiesNearAcrossRegions(cx,cz,1) half-open AABB test that
// PressurePlateBlock.getEntityCount is ported against. RNG-free.
//
//	[VERIFIED javap TripWireBlock.checkPressed(Level,BlockPos): AABB = state.getShape(level,pos).bounds()
//	 .move(pos); list = level.getEntities(null, AABB); -> checkPressed(level,pos,list).
//	 checkPressed(Level,BlockPos,List): bl2=false; for (Entity e : list) if (!e.isIgnoringBlockTriggers())
//	 { bl2=true; break; }.]
func (t *TickLoop) tripwireEntitiesPresent(pos pk.Position) bool {
	w := t.world()
	if w == nil {
		return false
	}
	state, ok := w.GetBlock(pos, dimMinY)
	if !ok || !block.IsTripwire(state) {
		return false
	}
	// state.getShape(level,pos).bounds().move(pos): the full-XZ footprint (0..1) with the ATTACHED-keyed
	// Y extent, translated to the block position.
	minY, maxY := tripwireShapeNotAttachedMinY, tripwireShapeNotAttachedMaxY
	if block.TripwireAttached(state) {
		minY, maxY = tripwireShapeAttachedMinY, tripwireShapeAttachedMaxY
	}
	loX := float64(pos.X)
	loZ := float64(pos.Z)
	hiX := float64(pos.X) + 1.0
	hiZ := float64(pos.Z) + 1.0
	loY := float64(pos.Y) + minY
	hiY := float64(pos.Y) + maxY

	// level.getEntities(null, AABB): every entity whose box intersects, no exclusion (null self). The
	// !isIgnoringBlockTriggers() filter is the three-arg checkPressed loop; first qualifying hit -> true.
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ) && !playerIsIgnoringBlockTriggers(p) {
			return true
		}
	}
	cx := float64(pos.X) + 0.5
	cz := float64(pos.Z) + 0.5
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if e == nil || e.dead {
			continue
		}
		ehw := e.width / 2
		if hiX <= e.x-ehw || e.x+ehw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ehw || e.z+ehw <= loZ {
			continue
		}
		if !entityIsIgnoringBlockTriggers(e) {
			return true
		}
	}
	return false
}

// entityIsIgnoringBlockTriggers ports Entity.isIgnoringBlockTriggers(): false for a base Entity, overridden
// by ArmorStand to return isMarker(). A marker armor stand does NOT press tripwires (nor pressure plates in
// vanilla). Every other v1 entity returns the base false. CITE: Entity.isIgnoringBlockTriggers (iconst_0);
// ArmorStand.isIgnoringBlockTriggers (return isMarker()).
func entityIsIgnoringBlockTriggers(e *Entity) bool {
	if e.typ == entity.ArmorStand.ID {
		return e.isArmorStandMarker()
	}
	return false
}

// playerIsIgnoringBlockTriggers: a Player is a base Entity (no isIgnoringBlockTriggers override), so it is
// always false. CITE: Entity.isIgnoringBlockTriggers (Player does not override).
func playerIsIgnoringBlockTriggers(_ *tickPlayer) bool { return false }

// ---------------------------------------------------------------------------------------------
// Powered rail + activator rail (PoweredRailBlock). ActivatorRailBlock IS `new PoweredRailBlock(...)`
// in vanilla, so both share this identical logic keyed off the POWERED property. The rail's POWERED
// bit is driven by redstone: a direct neighbor signal, OR a same-orientation powered rail up to 8
// cells away along the track line (a powered-rail "run"). Ported 1:1 from the 26.2 jar
// (temp/cache/26.2-inner.jar, javap this session):
//   PoweredRailBlock.updateState / findPoweredRailSignal / isSameRailWithPower.
// BaseRailBlock.neighborChanged and BaseRailBlock.onPlace both funnel into updateState; because the
// redstone neighbor-update worklist (server/redstone.go onRedstoneEdit -> drainRedstoneUpdates)
// enqueues the rail's own cell on any adjacent place/break, dispatching updateState from the
// IsPoweredRailBlock||IsActivatorRailBlock case there covers BOTH the neighborChanged and the onPlace
// entry points. The minecart physics (server/minecart.go) READS RailPowered; this is the write side.

// poweredRailUpdateState is PoweredRailBlock.updateState(state, level, pos, block):
//
//	boolean wasPowered = state.getValue(POWERED);
//	boolean flag = level.hasNeighborSignal(pos)
//	    || findPoweredRailSignal(level, pos, state, true, 0)
//	    || findPoweredRailSignal(level, pos, state, false, 0);
//	if (flag != wasPowered) {
//	    level.setBlock(pos, state.setValue(POWERED, flag), 3);
//	    level.updateNeighborsAt(pos.below(), this);
//	    if (state.getValue(SHAPE).isSlope()) level.updateNeighborsAt(pos.above(), this);
//	}
//
// The `level.getBlockState(pos) == state`-style guard is implicit: the caller passes the state read at
// pos this drain step, and SetBlock/broadcast only fire on a real change. CITE: PoweredRailBlock.updateState.
func (t *TickLoop) poweredRailUpdateState(pos pk.Position, state block.StateID, q *redstoneUpdateQueue) {
	wasPowered, ok := block.RailPowered(state)
	if !ok {
		return // not a POWERED-carrying rail (a plain Rail has none) -> nothing to update.
	}
	// `this` in the vanilla method is the concrete rail block updateState was invoked on (a powered_rail
	// or an activator_rail). isSameRailWithPower's `state.is(this)` requires the same kind, so a run does
	// not chain across the two families. We capture the origin kind and thread it through the walk.
	origin := block.IsActivatorRailBlock(state)
	flag := t.hasNeighborSignal(pos) ||
		t.findPoweredRailSignal(pos, state, origin, true, 0) ||
		t.findPoweredRailSignal(pos, state, origin, false, 0)
	if flag == wasPowered {
		return
	}
	newState, ok := block.SetRailPowered(state, flag)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
	}
	// updateNeighborsAt(pos.below(), this): the rail powers the block below (a powered rail is a weak
	// source out its underside via the redstone graph); enqueue it so a consumer there reacts.
	q.push(relative(pos, block.Down))
	// A slope rail (ASCENDING_*) also notifies the cell ABOVE. CITE: PoweredRailBlock.updateState
	// (SHAPE.isSlope() -> updateNeighborsAt(pos.above())).
	if shape, ok := block.RailShapeOf(state); ok && railShapeIsSlope(shape) {
		q.push(relative(pos, block.Up))
	}
}

// findPoweredRailSignal is PoweredRailBlock.findPoweredRailSignal(level, pos, state, dir, distance):
// walk the rail LINE one step in the direction implied by (SHAPE, dir), up to distance 8, testing
// isSameRailWithPower at the stepped cell (and, when the current cell is level ground i.e. NOT stepping
// down a slope, also one cell below it — the `flag` local). The per-shape step is the exact vanilla
// tableswitch (RailShape ordinal 0..5 -> switch cases 1..6):
//
//	NORTH_SOUTH:     dir ? z++ : z--
//	EAST_WEST:       dir ? x-- : x++
//	ASCENDING_EAST:  dir ? x--            : (x++, y++, flag=false); shape=EAST_WEST
//	ASCENDING_WEST:  dir ? (x--, y++, flag=false) : x++;           shape=EAST_WEST
//	ASCENDING_NORTH: dir ? z++            : (z--, y++, flag=false); shape=NORTH_SOUTH
//	ASCENDING_SOUTH: dir ? (z++, y++, flag=false) : z--;           shape=NORTH_SOUTH
//
// `flag` (istore 9, seeded true) means "also probe one cell below the stepped position" — true on the
// flat step and on ascending in the non-rising direction, false when the step itself rises. CITE:
// PoweredRailBlock.findPoweredRailSignal + PoweredRailBlock$1 SwitchMap.
func (t *TickLoop) findPoweredRailSignal(pos pk.Position, state block.StateID, originActivator, dir bool, distance int) bool {
	if distance >= 8 {
		return false
	}
	x, y, z := pos.X, pos.Y, pos.Z
	flag := true
	shape, ok := block.RailShapeOf(state)
	if !ok {
		return false
	}
	switch shape {
	case block.RailShapeNorthSouth: // case 1
		if dir {
			z++
		} else {
			z--
		}
	case block.RailShapeEastWest: // case 2
		if dir {
			x--
		} else {
			x++
		}
	case block.RailShapeAscendingEast: // case 3
		if dir {
			x--
		} else {
			x++
			y++
			flag = false
		}
		shape = block.RailShapeEastWest
	case block.RailShapeAscendingWest: // case 4
		if dir {
			x--
			y++
			flag = false
		} else {
			x++
		}
		shape = block.RailShapeEastWest
	case block.RailShapeAscendingNorth: // case 5
		if dir {
			z++
		} else {
			z--
			y++
			flag = false
		}
		shape = block.RailShapeNorthSouth
	case block.RailShapeAscendingSouth: // case 6
		if dir {
			z++
			y++
			flag = false
		} else {
			z--
		}
		shape = block.RailShapeNorthSouth
	}
	stepped := pk.Position{X: x, Y: y, Z: z}
	if t.isSameRailWithPower(stepped, originActivator, dir, distance, shape) {
		return true
	}
	if flag && t.isSameRailWithPower(pk.Position{X: x, Y: y - 1, Z: z}, originActivator, dir, distance, shape) {
		return true
	}
	return false
}

// isSameRailWithPower is PoweredRailBlock.isSameRailWithPower(level, pos, dir, distance, shape):
//
//	BlockState state = level.getBlockState(pos);
//	if (!state.is(this)) return false;                 // must be the SAME rail block type
//	RailShape rs = state.getValue(SHAPE);
//	if (shape == EAST_WEST && (rs == NORTH_SOUTH || rs == ASCENDING_NORTH || rs == ASCENDING_SOUTH)) return false;
//	if (shape == NORTH_SOUTH && (rs == EAST_WEST || rs == ASCENDING_EAST || rs == ASCENDING_WEST)) return false;
//	if (!state.getValue(POWERED)) return false;
//	return level.hasNeighborSignal(pos) || findPoweredRailSignal(level, pos, state, dir, distance + 1);
//
// The `state.is(this)` check must be the SAME concrete block type as the rail the run started from:
// `this` stays the origin PoweredRailBlock instance across the whole recursion, so a powered_rail run
// never chains through an activator_rail and vice-versa. `originActivator` carries that origin kind.
// CITE: PoweredRailBlock.isSameRailWithPower.
func (t *TickLoop) isSameRailWithPower(pos pk.Position, originActivator, dir bool, distance int, shape block.RailShape) bool {
	state := t.redstoneBlockAt(pos)
	// state.is(this): the neighbour must be the SAME concrete rail block as the origin.
	if block.IsActivatorRailBlock(state) != originActivator || !block.IsPoweredRailFamily(state) {
		return false
	}
	rs, ok := block.RailShapeOf(state)
	if !ok {
		return false
	}
	// shape guard: a run following an EAST_WEST orientation must not chain to a NORTH_SOUTH-family rail
	// (and vice-versa). CITE: PoweredRailBlock.isSameRailWithPower shape checks.
	if shape == block.RailShapeEastWest &&
		(rs == block.RailShapeNorthSouth || rs == block.RailShapeAscendingNorth || rs == block.RailShapeAscendingSouth) {
		return false
	}
	if shape == block.RailShapeNorthSouth &&
		(rs == block.RailShapeEastWest || rs == block.RailShapeAscendingEast || rs == block.RailShapeAscendingWest) {
		return false
	}
	p, ok := block.RailPowered(state)
	if !ok || !p {
		return false
	}
	return t.hasNeighborSignal(pos) || t.findPoweredRailSignal(pos, state, originActivator, dir, distance+1)
}

// ---------------------------------------------------------------------------------------------
// Copper bulb (CopperBulbBlock) — a redstone T-flip-flop. A RISING edge on POWERED toggles LIT; a held
// signal does not re-toggle (POWERED stays true). Ported 1:1 from the 26.2 jar:
//   CopperBulbBlock.checkAndFlip / neighborChanged / onPlace.
// LIT is also the comparator analog output (LIT?15:0) — wired in redstone_diode.go. Both LIT and
// POWERED are real state properties in the generated table (level/block/blocks.go), so this is
// wire-correct with no runtime flag.

// copperBulbCheckAndFlip is CopperBulbBlock.checkAndFlip(state, level, pos):
//
//	boolean flag = level.hasNeighborSignal(pos);
//	if (flag == state.getValue(POWERED)) return;               // no edge -> nothing changes
//	BlockState s = state;
//	if (!state.getValue(POWERED)) s = state.cycle(LIT);        // RISING edge (false->true): toggle LIT
//	level.setBlock(pos, s.setValue(POWERED, flag), 3);
//
// So: on a rising edge, LIT flips and POWERED becomes true; on a falling edge, LIT is untouched and
// POWERED becomes false. This is the T-flip-flop: two rising edges to return LIT to its start. The
// COPPER_BULB_TURN_ON/OFF sound is a client cosmetic (cite-deferred). Dispatched from the IsCopperBulb
// case in drainRedstoneUpdates (server/redstone.go), which covers BOTH neighborChanged and onPlace
// (the redstone worklist enqueues the bulb's own cell on any adjacent edit). CITE:
// CopperBulbBlock.checkAndFlip.
func (t *TickLoop) copperBulbCheckAndFlip(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	flag := t.hasNeighborSignal(pos)
	if flag == block.BulbPowered(state) {
		return
	}
	s := state
	if !block.BulbPowered(state) {
		// RISING edge: state.cycle(LIT) — flip LIT.
		if cycled, ok := block.BulbWithLit(state, !block.BulbLit(state)); ok {
			s = cycled
		}
	}
	newState, ok := block.BulbWithPowered(s, flag)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
	}
}

// copperBulbAnalogOutputSignal ports CopperBulbBlock.getAnalogOutputSignal (hasAnalogOutputSignal ==
// true): LIT ? 15 : 0. Returns (signal, true) when pos is a copper_bulb, (0, false) otherwise (so the
// comparator keeps its super/container value). CITE: CopperBulbBlock.hasAnalogOutputSignal /
// getAnalogOutputSignal.
func (t *TickLoop) copperBulbAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsCopperBulb(state) {
		return 0, false
	}
	if block.BulbLit(state) {
		return 15, true
	}
	return 0, true
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
