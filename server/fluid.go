package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid.go (GAMEPLAY-05) ports the vanilla FlowingFluid water simulation
// (net.minecraft.world.level.material.FlowingFluid / WaterFluid) — the level encoding, the
// flow constants, the getNewLiquid/spread/spreadToSides/getSlopeDistance algorithm, and the
// tickFluids dispatcher that drains the scheduled-tick queue (fluid_schedule.go). It
// OVERWRITES the 17-01 stub (which carried an empty tickFluids + a `type fluidScheduleQueue
// struct{}` placeholder — the type now lives in fluid_schedule.go, defined exactly once).
//
// PORT NOTES (no GPL paste — bytecode behavior translated to idiomatic Go, each method cites
// its jar source). The model is the air-or-same-fluid passability subset: a cell is passable
// for fluid if it is air or already this fluid (the full VoxelShape "wall" occlusion of
// canPassThroughWall is simplified to "not a solid block" for the v1 water gate — 17-RESEARCH
// Pattern 5). Source conversion uses the gamerule default (waterSourceConversion = true).
//
// All access is on the tick goroutine over the tick-owned ChunkManager (TICK-05); world writes
// go through world.SetBlock, the sole mutator (T-17-06).

// Water flow constants — VERIFIED from temp/cache/26.2-inner.jar:
//
//	javap net.minecraft.world.level.material.WaterFluid:
//	  getSlopeFindDistance() -> iconst_4  (4)
//	  getDropOff()           -> iconst_1  (1)
//	  getTickDelay()         -> iconst_5  (5)
//	  getAmount(source)      -> 8         (FlowingFluid: source amount is 8)
const (
	waterDropOff           = 1 // FlowingFluid.getDropOff (WaterFluid -> 1): horizontal level decrement
	waterTickDelay         = 5 // FlowingFluid.getTickDelay (WaterFluid -> 5): scheduled-tick spacing
	waterSlopeFindDistance = 4 // FlowingFluid.getSlopeFindDistance (WaterFluid -> 4): slope-find radius
	waterSourceAmount      = 8 // FlowingFluid: a source fluid has amount 8 (full)
)

// waterSourceConversion is the gamerule default (GameRules.WATER_SOURCE_CONVERSION). With no
// gamerule engine yet, water source conversion is enabled to match vanilla defaults
// (WaterFluid.canConvertToSource reads this gamerule, default true).
const waterSourceConversion = true

// getLegacyLevel maps a fluid (amount, falling, source) to the wire "level" property carried by
// block.Water{Level}. PORT of FlowingFluid.getLegacyLevel (verified bytecode):
//
//	isSource ? 0 : (8 - min(amount, 8)) + (falling ? 8 : 0)
//
// So: source -> 0; flowing amount a -> 8-a (a=8 full -> 0, a=1 -> 7); falling adds 8.
func getLegacyLevel(amount int, falling, source bool) int {
	if source {
		return 0
	}
	if amount > waterSourceAmount {
		amount = waterSourceAmount
	}
	level := waterSourceAmount - amount
	if falling {
		level += 8
	}
	return level
}

// waterLevelOf reads the legacy water level (0=source, 1..7 flowing, 8..15 falling) of a state
// id, reporting isWater=false for any non-water block. Source:
// level/block/block.go (StateList) + blocks.go (block.Water{Level Integer}).
func waterLevelOf(id block.StateID) (level int, isWater bool) {
	if id < 0 || int(id) >= len(block.StateList) {
		return 0, false
	}
	if w, ok := block.StateList[id].(block.Water); ok {
		return int(w.Level), true
	}
	return 0, false
}

// waterStateID returns the state id for water at the given legacy level. Source:
// block.ToStateID[block.Water{Level Integer}].
func waterStateID(level int) block.StateID {
	return block.ToStateID[block.Water{Level: block.Integer(level)}]
}

// airStateID is the empty-cell target (a fluid that drains becomes air).
func airStateID() block.StateID {
	return block.ToStateID[block.Air{}]
}

// fluidState is the decoded fluid at a position: whether it is this fluid at all, its source
// flag, its falling flag, and its amount (1..8; 8 = full/source). The amount/falling/source
// trio is the input to getLegacyLevel and the spread arithmetic. For water, the wire "level"
// decodes back to amount via: source -> amount 8; falling -> amount = 8 - (legacy-8); flowing
// -> amount = 8 - legacy.
type fluidState struct {
	isWater bool
	source  bool
	falling bool
	amount  int // 1..8; the FlowingFluid "amount" (8 = full source-equivalent height)
}

// decodeFluid reads the fluid at a state id. The legacy "level" property is inverted back to
// the FlowingFluid (amount, falling, source) trio (the inverse of getLegacyLevel):
//
//	level 0       -> source (amount 8)
//	level 1..7    -> flowing, amount = 8 - level, falling=false
//	level 8..15   -> falling, amount = 8 - (level - 8) = 16 - level, falling=true
func decodeFluid(id block.StateID) fluidState {
	legacy, ok := waterLevelOf(id)
	if !ok {
		return fluidState{}
	}
	switch {
	case legacy == 0:
		return fluidState{isWater: true, source: true, amount: waterSourceAmount}
	case legacy <= 7:
		return fluidState{isWater: true, amount: waterSourceAmount - legacy}
	default: // 8..15 falling
		return fluidState{isWater: true, falling: true, amount: waterSourceAmount - (legacy - 8)}
	}
}

// encodeFluid turns a fluidState into the state id to write (air when not water).
func encodeFluid(f fluidState) block.StateID {
	if !f.isWater {
		return airStateID()
	}
	return waterStateID(getLegacyLevel(f.amount, f.falling, f.source))
}

// tickFluids is the GAMEPLAY-05 fluid pass, called from tickWorld each tick (wired by 17-01 in
// tick_phases.go — not edited here). It lazily constructs the scheduled-tick queue (nil-check,
// so SetWorld/tick.go is never touched), drains the bucket due this gametime in deterministic
// packed-pos order, and runs FlowingFluid.tick (fluidTick) per scheduled position.
func (t *TickLoop) tickFluids() {
	if t.fluidSchedule == nil {
		t.fluidSchedule = newFluidScheduleQueue()
	}
	if t.world == nil {
		return
	}
	for _, st := range t.fluidSchedule.drainDue(t.gametime) {
		t.fluidTick(st.pos)
	}
}

// scheduleFluidTick schedules pos to re-evaluate getTickDelay(=5) ticks from now — the
// getTickDelay-spaced propagation (never a per-tick full-water scan). Lazily inits the queue.
func (t *TickLoop) scheduleFluidTick(pos pk.Position) {
	if t.fluidSchedule == nil {
		t.fluidSchedule = newFluidScheduleQueue()
	}
	t.fluidSchedule.schedule(pos, t.gametime+waterTickDelay)
}

// fluidTick is the per-position FlowingFluid.tick body. Task 1 ships the level/schedule
// infrastructure; Task 2 fills this with the real getNewLiquid + spread port. Until then it is
// a no-op so the dispatcher compiles and the schedule/level tests run in isolation.
func (t *TickLoop) fluidTick(pos pk.Position) {
	_ = pos
}
