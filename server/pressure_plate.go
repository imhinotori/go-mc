// pressure_plate.go -- PLATE-01: the pressure-plate redstone INPUT, a 1:1 port of vanilla Java 26.2
// (protocol 776) BasePressurePlateBlock + PressurePlateBlock + WeightedPressurePlateBlock, verified
// method-for-method against temp/cache/26.2-inner.jar via javap this session (no GPL paste).
//
// An entity standing on a plate presses it (POWERED/POWER + a full redstone signal out every face,
// strong UP); when the entity leaves, a scheduled tick (getPressedTime later) unpresses it. Families:
//   - PressurePlateBlock: stone/polished_blackstone use MOBS sensitivity (only a LivingEntity triggers),
//     the wooden variants use EVERYTHING (any Entity, includes dropped items); POWERED boolean,
//     getSignalForState 0/15, getPressedTime 20.
//   - WeightedPressurePlateBlock: light maxWeight 15 / heavy maxWeight 150; EVERYTHING sensitivity;
//     POWER = Mth.ceil(min(maxWeight, count)/maxWeight * 15), getPressedTime 10.
//
// The signal EMISSION (stateGetSignal / stateGetDirectSignal / isSignalSource) is wired in redstone.go;
// this file owns the PRESS detection (the per-tick entity scan + checkPressed) and the unpress tick.
//
// PIG-ORACLE: detection is gated on a plate at an entity feet. No plate near the oracle pig -> the
// feet-block lookup is never a plate -> plateEntityInsideAt returns early -> zero new behavior and zero
// new RNG draws. Byte-identical.
//
// VERIFIED javap: BasePressurePlateBlock (TOUCH_AABB = Block.column(14,0,4).toAabbs().getFirst() ->
// inset 1/16, height 0..4/16; getPressedTime 20; entityInside fires checkPressed when getSignalForState
// == 0; tick fires checkPressed when getSignalForState > 0; getDirectSignal UP-only; isSignalSource true;
// checkPressed writes setSignalForState + updateNeighbours(pos, pos.below) then schedules getPressedTime).
// PressurePlateBlock (POWERED bool; getSignalStrength sensitivity switch EVERYTHING->Entity / MOBS->
// LivingEntity, count>0 ? 15 : 0). WeightedPressurePlateBlock (POWER int; getPressedTime 10; i = min(
// getEntityCount(Entity), maxWeight); i>0 ? Mth.ceil((float)i/maxWeight * 15) : 0).
package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	plateTouchInset     = 0.0625
	plateTouchHeight    = 0.25
	platePressedTime    = 20
	weightedPressedTime = 10
)

const plateTickType blockTickType = "minecraft:pressure_plate"

func (t *TickLoop) tickPressurePlates() {
	if t.world() == nil {
		return
	}
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		t.plateEntityInsideAt(blockPosOf(p.x, p.y, p.z))
	}
	for _, e := range t.cur().entities.all() {
		if e == nil || e.dead {
			continue
		}
		t.plateEntityInsideAt(blockPosOf(e.x, e.y, e.z))
	}
}

func (t *TickLoop) plateEntityInsideAt(pos pk.Position) {
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return
	}
	if !block.IsPressurePlate(state) && !block.IsWeightedPressurePlate(state) {
		return
	}
	if t.plateGetSignalForState(state) == 0 {
		t.plateCheckPressed(pos, state)
	}
}

func (t *TickLoop) plateGetSignalForState(state block.StateID) int {
	if block.IsWeightedPressurePlate(state) {
		return block.WeightedPressurePlatePower(state)
	}
	if block.PressurePlatePowered(state) {
		return 15
	}
	return 0
}

func (t *TickLoop) plateSetSignalForState(state block.StateID, i int) (block.StateID, bool) {
	if block.IsWeightedPressurePlate(state) {
		return block.WeightedPressurePlateWithPower(state, i)
	}
	return block.PressurePlateWithPowered(state, i > 0)
}

func (t *TickLoop) plateGetSignalStrength(pos pk.Position, state block.StateID) int {
	if block.IsWeightedPressurePlate(state) {
		maxWeight := block.WeightedPressurePlateMaxWeight(state)
		count := t.plateEntityCount(pos, false)
		i := count
		if i > maxWeight {
			i = maxWeight
		}
		if i <= 0 {
			return 0
		}
		frac := float32(i) / float32(maxWeight)
		return mthCeilF(frac * 15.0)
	}
	mobsOnly := block.PressurePlateMobsOnly(state)
	if t.plateEntityCount(pos, mobsOnly) > 0 {
		return 15
	}
	return 0
}

func (t *TickLoop) plateEntityCount(pos pk.Position, mobsOnly bool) int {
	loX := float64(pos.X) + plateTouchInset
	loY := float64(pos.Y)
	loZ := float64(pos.Z) + plateTouchInset
	hiX := float64(pos.X) + 1.0 - plateTouchInset
	hiY := float64(pos.Y) + plateTouchHeight
	hiZ := float64(pos.Z) + 1.0 - plateTouchInset

	count := 0
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ) {
			count++
		}
	}
	cx := float64(pos.X) + 0.5
	cz := float64(pos.Z) + 0.5
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if e == nil || e.dead {
			continue
		}
		if mobsOnly && !isLivingMob(e) {
			continue
		}
		ihw := e.width / 2
		if hiX <= e.x-ihw || e.x+ihw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ihw || e.z+ihw <= loZ {
			continue
		}
		count++
	}
	return count
}

func (t *TickLoop) plateCheckPressed(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	current := t.plateGetSignalForState(state)
	i := t.plateGetSignalStrength(pos, state)

	shouldBePressed := i > 0

	if current != i {
		newState, ok := t.plateSetSignalForState(state, i)
		if ok && t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
			t.onRedstoneEdit(pos)
			t.onRedstoneEdit(relative(pos, block.Down))
		}
	}

	if shouldBePressed {
		delay := platePressedTime
		if block.IsWeightedPressurePlate(state) {
			delay = weightedPressedTime
		}
		if !t.hasScheduledBlockTick(pos, plateTickType) {
			t.scheduleBlockTick(pos, plateTickType, delay)
		}
	}
}

func (t *TickLoop) plateTick(state block.StateID, pos pk.Position) {
	if t.plateGetSignalForState(state) > 0 {
		t.plateCheckPressed(pos, state)
	}
}

func mthCeilF(v float32) int {
	i := int(v)
	if float32(i) < v {
		return i + 1
	}
	return i
}
