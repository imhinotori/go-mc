package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid.go (GAMEPLAY-05) ports the vanilla FlowingFluid water simulation
// (net.minecraft.world.level.material.FlowingFluid / WaterFluid) - the level encoding, the
// flow constants, the getNewLiquid/spread/spreadToSides/getSlopeDistance algorithm, and the
// tickFluids dispatcher that drains the scheduled-tick queue (fluid_schedule.go). It
// OVERWRITES the 17-01 stub (which carried an empty tickFluids + a `type fluidScheduleQueue
// struct{}` placeholder - the type now lives in fluid_schedule.go, defined exactly once).
//
// PORT NOTES (no GPL paste - bytecode behavior translated to idiomatic Go, each method cites
// its jar source). The model is the air-or-same-fluid passability subset: a cell is passable
// for fluid if it is air or already this fluid (the full VoxelShape "wall" occlusion of
// canPassThroughWall is simplified to "not a solid block" for the v1 water gate - 17-RESEARCH
// Pattern 5). Source conversion uses the gamerule default (waterSourceConversion = true).
//
// All access is on the tick goroutine over the tick-owned ChunkManager (TICK-05); world writes
// go through world.SetBlock, the sole mutator (T-17-06).

// Water flow constants - VERIFIED from temp/cache/26.2-inner.jar:
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

	// Lava (OVERWORLD, non-fast) flow constants - VERIFIED from temp/cache/26.2-inner.jar:
	//
	//	javap net.minecraft.world.level.material.LavaFluid (isFastLava == false branch, i.e. the
	//	overworld/default dimension; fast lava is the nether's ultrawarm FAST_LAVA env attribute):
	//	  getDropOff(LevelReader)           -> isFastLava ? iconst_1 : iconst_2  => 2
	//	  getTickDelay(LevelReader)         -> isFastLava ? bipush 10 : bipush 30 => 30
	//	  getSlopeFindDistance(LevelReader) -> isFastLava ? iconst_4 : iconst_2  => 2
	// Source amount 8 is shared (FlowingFluid). getDropOff=2 gives lava its shorter 3-block spread
	// (8 -> 6 -> 4 -> 2 -> 0) vs water's 7 (dropOff 1). Sulfur v1 targets the overworld only, so the
	// non-fast constants are baked (isFastLava is an EnvironmentAttribute read; the fast/nether split
	// is a later per-dimension concern - cite LavaFluid.isFastLava).
	lavaDropOff           = 2  // LavaFluid.getDropOff (overworld)
	lavaTickDelay         = 30 // LavaFluid.getTickDelay (overworld)
	lavaSlopeFindDistance = 2  // LavaFluid.getSlopeFindDistance (overworld)

	// maxFluidTicksPerTick caps how many scheduled fluid cells we drain in ONE logical tick - the
	// Go analogue of LevelTicks.tick(long gameTime, int maxAllowedTicks) (ServerLevel passes 65536
	// and collectTicks/canScheduleMoreTicks stops once the cap is hit, leaving the rest for next
	// tick). Without a cap, a batch of freshly-streamed chunks whose aquifer border cells are all
	// post-processed at the same gametime (postProcessChunkFluids enqueues each at gametime+1) drains
	// thousands of cells in a single tick - observed 17498 cells = a ~13s tick stall. Capping +
	// re-enqueuing the overflow to the next gametime spreads the work across ticks WITHOUT changing
	// the simulation (every cell still ticks, in the same deterministic packed-pos order - drainDue
	// sorts the bucket) - a pure perf bound, the only permitted deviation. 65536 matches vanilla.
	maxFluidTicksPerTick = 65536
)

// waterSourceConversion is the gamerule default (GameRules.WATER_SOURCE_CONVERSION). With no
// gamerule engine yet, water source conversion is enabled to match vanilla defaults
// (WaterFluid.canConvertToSource reads this gamerule, default true).
const waterSourceConversion = true

// lavaSourceConversion is the gamerule default (GameRules.LAVA_SOURCE_CONVERSION). LavaFluid.
// canConvertToSource reads this gamerule, whose vanilla default is FALSE - lava does NOT form new
// source blocks from two adjacent sources (unlike water). Baked as false until a gamerule engine
// exists. Cite: net.minecraft.world.level.material.LavaFluid.canConvertToSource.
const lavaSourceConversion = false

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

// lavaLevelOf reads the legacy lava level (0=source, 1..7 flowing, 8..15 falling) of a state id,
// reporting isLava=false for any non-lava block. The exact mirror of waterLevelOf but matching the
// generated block.Lava{Level Integer} (level/block/blocks.go: `Lava struct { Level Integer }`,
// ID "minecraft:lava"). Cite net.minecraft.world.level.material.LavaFluid / the fluid registry:
// lava carries the SAME legacy "level" property as water (FlowingFluid.getLegacyLevel is shared),
// so the level→amount inversion in decodeFluid is identical; only the FLOW constants differ (lava's
// overworld getDropOff -> iconst_2 = 2 vs water's 1, javap-verified), which is a flow-sim concern
// DEFERRED this phase (decode-only satisfies MOB-SUB-05; FloatGoal only READS lava, never flows it).
func lavaLevelOf(id block.StateID) (level int, isLava bool) {
	if id < 0 || int(id) >= len(block.StateList) {
		return 0, false
	}
	if l, ok := block.StateList[id].(block.Lava); ok {
		return int(l.Level), true
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
	// isLava marks a lava cell (block.Lava). A cell is water XOR lava XOR neither - isWater and
	// isLava are never both true. The decode is the full-lava read MOB-SUB-05 needs (do NOT stub
	// lava as const-false). Cite net.minecraft.world.level.material.LavaFluid / FluidTags.LAVA.
	// Lava FLOW SIMULATION is DEFERRED (FlowingFluid.tick for lava): no existing .isWater consumer
	// reads isLava, and the water flow scheduler keeps its `if fs.isWater` guards, so lava is never
	// flowed by the water sim - the extension is decode-only and water behavior is unperturbed.
	isLava  bool
	source  bool
	falling bool
	amount  int // 1..8; the FlowingFluid "amount" (8 = full source-equivalent height)
}

// isFluid reports whether this cell holds ANY flowing fluid (water OR lava). Replaces the
// water-only `f.isWater` gate now that the sim runs both fluids through the shared FlowingFluid
// geometry.
func (f fluidState) isFluid() bool { return f.isWater || f.isLava }

// sameKind reports whether two states are the same fluid TYPE (both water, both lava, or both
// non-fluid). FlowingFluid.isSame - a cell only interacts (merges, converts to source, is
// replaced by a stronger flow) with its OWN fluid; water and lava never merge.
func (f fluidState) sameKind(o fluidState) bool {
	return f.isWater == o.isWater && f.isLava == o.isLava
}

// makeFluid builds a fluidState of the same KIND as f (used to synthesize down/side flow states
// that inherit the flowing fluid's type).
func (f fluidState) makeFluid(amount int, falling, source bool) fluidState {
	return fluidState{isWater: f.isWater, isLava: f.isLava, source: source, falling: falling, amount: amount}
}

// dropOff / tickDelay / slopeFindDistance / sourceConversion return the per-KIND flow constants.
// Water and lava share FlowingFluid's geometry but differ in these four numbers (LavaFluid
// overrides getDropOff/getTickDelay/getSlopeFindDistance/canConvertToSource - javap-verified).
func (f fluidState) dropOff() int {
	if f.isLava {
		return lavaDropOff
	}
	return waterDropOff
}
func (f fluidState) tickDelay() int {
	if f.isLava {
		return lavaTickDelay
	}
	return waterTickDelay
}
func (f fluidState) slopeFindDistance() int {
	if f.isLava {
		return lavaSlopeFindDistance
	}
	return waterSlopeFindDistance
}
func (f fluidState) sourceConversion() bool {
	if f.isLava {
		return lavaSourceConversion
	}
	return waterSourceConversion
}

// decodeFluid reads the fluid at a state id. The legacy "level" property is inverted back to
// the FlowingFluid (amount, falling, source) trio (the inverse of getLegacyLevel):
//
//	level 0       -> source (amount 8)
//	level 1..7    -> flowing, amount = 8 - level, falling=false
//	level 8..15   -> falling, amount = 8 - (level - 8) = 16 - level, falling=true
func decodeFluid(id block.StateID) fluidState {
	if legacy, ok := waterLevelOf(id); ok {
		switch {
		case legacy == 0:
			return fluidState{isWater: true, source: true, amount: waterSourceAmount}
		case legacy <= 7:
			return fluidState{isWater: true, amount: waterSourceAmount - legacy}
		default: // 8..15 falling
			return fluidState{isWater: true, falling: true, amount: waterSourceAmount - (legacy - 8)}
		}
	}
	// Lava branch (MOB-SUB-05 full-lava decode). FlowingFluid.getLegacyLevel is SHARED by Water and
	// Lava, so the level→amount inversion is IDENTICAL to water above - only the isLava/isWater flag
	// differs. Decode-only: the lava amount/falling trio is read by mobInLava / mobFluidHeight(LAVA);
	// the differing lava flow constants (getDropOff=2 overworld, javap LavaFluid) belong to the
	// DEFERRED lava flow sim, not the decode. A non-lava, non-water id returns the zero fluidState.
	if legacy, ok := lavaLevelOf(id); ok {
		switch {
		case legacy == 0:
			return fluidState{isLava: true, source: true, amount: waterSourceAmount}
		case legacy <= 7:
			return fluidState{isLava: true, amount: waterSourceAmount - legacy}
		default: // 8..15 falling
			return fluidState{isLava: true, falling: true, amount: waterSourceAmount - (legacy - 8)}
		}
	}
	return fluidState{}
}

// lavaStateID returns the state id for lava at the given legacy level. Source:
// block.ToStateID[block.Lava{Level Integer}]. Mirrors waterStateID.
func lavaStateID(level int) block.StateID {
	return block.ToStateID[block.Lava{Level: block.Integer(level)}]
}

// encodeFluid turns a fluidState into the state id to write (air when neither water nor lava).
// The lava branch is defensive: the water flow sim NEVER feeds a lava fluidState here (every
// spread/tick path is `if fs.isWater`-guarded - see the consumer audit), so this is unreachable
// from the current scheduler. It is written 1:1 so the DEFERRED lava flow sim can route through
// the same primitive without re-deriving the encode, and so a lava state can never silently
// collapse to air. getLegacyLevel is shared by both fluids (FlowingFluid.getLegacyLevel).
func encodeFluid(f fluidState) block.StateID {
	switch {
	case f.isWater:
		return waterStateID(getLegacyLevel(f.amount, f.falling, f.source))
	case f.isLava:
		return lavaStateID(getLegacyLevel(f.amount, f.falling, f.source))
	default:
		return airStateID()
	}
}

// tickFluids is the GAMEPLAY-05 fluid pass, called from tickWorld each tick (wired by 17-01 in
// tick_phases.go - not edited here). It lazily constructs the scheduled-tick queue (nil-check,
// so SetWorld/tick.go is never touched), drains the bucket due this gametime in deterministic
// packed-pos order, and runs FlowingFluid.tick (fluidTick) per scheduled position.
func (t *TickLoop) tickFluids() {
	if t.cur().fluidSchedule == nil {
		t.cur().fluidSchedule = newFluidScheduleQueue()
	}
	if t.world() == nil {
		return
	}
	due := t.cur().fluidSchedule.drainDue(t.gametime)
	// Per-tick budget (LevelTicks.tick maxAllowedTicks=65536): process at most the cap this tick and
	// re-enqueue the overflow to the NEXT gametime so a chunk-load batch can't dump thousands of
	// aquifer cells into one tick (the ~13s stall). drainDue already returned the bucket in
	// deterministic packed-pos order, so the kept prefix + the deferred suffix preserve ordering -
	// the simulation is unchanged, only spread across ticks (a pure perf bound).
	if len(due) > maxFluidTicksPerTick {
		overflow := due[maxFluidTicksPerTick:]
		due = due[:maxFluidTicksPerTick]
		next := t.gametime + 1
		for _, st := range overflow {
			t.cur().fluidSchedule.schedule(st.pos, next)
		}
	}
	for _, st := range due {
		t.fluidTick(st.pos)
	}
	// Cost instrumentation for the "should the fluid sim move off-tick?" question: emit the
	// per-gametick fluid work (cells processed this tick + the queue depth still pending) ONLY
	// when there was work, so a busy session's fluid load is greppable (`grep 'ULTRA\[fluid\] cost'`)
	// without deciding the async optimization blind. Zero cost when the firehose is off.
	if len(due) > 0 {
		udebug("fluid", "cost gametime=%d processed=%d pendingAfter=%d", t.gametime, len(due), t.cur().fluidSchedule.pending())
	}
}

// postProcessChunkFluids ports LevelChunk.postProcessGeneration's fluid step: when a freshly
// generated chunk goes live, run FluidState.tick EXACTLY ONCE on each cell the aquifer flagged
// for post-processing during fill (Chunk.PostProcessFluids = the markPosForPostProcessing list).
//
// This is the SAFE replacement for the cascading scan-and-schedule that was reverted twice. It
// does NOT scan all fluid cells, and it does NOT put cells on the recurring scheduleFluidTick
// queue at gen time - it kicks ONLY the small set of UNSTABLE BORDER cells the aquifer marked
// (shouldScheduleFluidUpdate), exactly once. That one fluidTick recomputes each border cell's
// liquid and spreads it (the normal getTickDelay-spaced queue takes over from there), which is
// what lets generated cave/aquifer water flow into a bordering air gap on load - without the
// runaway cascade (the aquifer only flags discontinuous borders, not entire flooded caves).
// Cite: net.minecraft.world.level.chunk.LevelChunk.postProcessGeneration ->
// FluidState.tick(level, pos, state) once per marked pos.
func (t *TickLoop) postProcessChunkFluids(pos level.ChunkPos, ch *level.Chunk) {
	if ch == nil || len(ch.PostProcessFluids) == 0 || t.world() == nil {
		return
	}
	if t.cur().fluidSchedule == nil {
		t.cur().fluidSchedule = newFluidScheduleQueue()
	}
	baseX := int(pos[0]) * 16
	baseZ := int(pos[1]) * 16
	for _, packed := range ch.PostProcessFluids {
		lx := int(packed & 0xF)
		lz := int((packed >> 4) & 0xF)
		localY := int(packed >> 8)
		wp := pk.Position{X: baseX + lx, Y: dimMinY + localY, Z: baseZ + lz}
		// ENQUEUE the one-shot kick for the next fluid pass rather than firing it inline here.
		// chunkReady.applyTo integrates chunks ONE AT A TIME; a flagged border cell that must
		// flow ACROSS the chunk edge needs the neighbor chunk loaded, but the neighbor may not be
		// integrated yet when THIS chunk goes live. Firing inline read the neighbor as unloaded
		// and the spread stopped at the border (the operator's "natural water doesn't expand").
		// Deferring to the queue lets the whole load batch settle first; the cell still gets
		// exactly one FluidState.tick (just one pass later - invisible, fluid getTickDelay is 5).
		// spread() inside that tick re-schedules onward flow normally. CITE: LevelChunk.
		// postProcessGeneration runs FluidState.tick once per flagged cell (timing relaxed to the
		// next pass to honor the cross-chunk neighbor precondition vanilla gets from full-status).
		t.cur().fluidSchedule.schedule(wp, t.gametime+1)
	}
}

// scheduleFluidTick schedules pos to re-evaluate getTickDelay(=5) ticks from now - the
// getTickDelay-spaced propagation (never a per-tick full-water scan). Lazily inits the queue.
func (t *TickLoop) scheduleFluidTick(pos pk.Position) {
	t.scheduleFluidTickKind(pos, t.fluidAt(pos))
}

// scheduleFluidTickKind schedules pos to re-evaluate getTickDelay ticks from now using the delay
// of the GIVEN fluid kind (water 5, lava 30 - LavaFluid.getTickDelay). Callers that already hold
// the decoded fluid pass it directly; scheduleFluidTick re-reads the cell for callers that don't.
// A non-fluid (empty) cell falls back to the water delay - harmless, the cell no-ops when ticked.
func (t *TickLoop) scheduleFluidTickKind(pos pk.Position, f fluidState) {
	if t.cur().fluidSchedule == nil {
		t.cur().fluidSchedule = newFluidScheduleQueue()
	}
	delay := waterTickDelay
	if f.isFluid() {
		delay = f.tickDelay()
	}
	t.cur().fluidSchedule.schedule(pos, t.gametime+int64(delay))
}

// scheduleEmpty reports whether the fluid queue has no pending ticks (a fixed point). Used by
// tests to drain the simulation to completion and prove termination.
// scheduleFluidTickDelay schedules pos exactly `delay` ticks out (the getSpreadDelay result), for
// the fluid KIND of f. The kind picks nothing here (delay is precomputed) but is kept for symmetry.
func (t *TickLoop) scheduleFluidTickDelay(pos pk.Position, f fluidState, delay int) {
	if t.cur().fluidSchedule == nil {
		t.cur().fluidSchedule = newFluidScheduleQueue()
	}
	if delay < 0 {
		delay = 0
	}
	t.cur().fluidSchedule.schedule(pos, t.gametime+int64(delay))
}

// getSpreadDelay ports FlowingFluid.getSpreadDelay(level, pos, oldState, newState). WaterFluid does
// not override it, so it is getTickDelay (5). LavaFluid overrides it: when BOTH oldState and newState
// are non-empty, non-falling lava AND oldState.getHeight() > newState.getHeight(), it draws
// random.nextInt(4) and, if != 0, multiplies the delay by 4 (a slower re-tick when lava is receding
// into taller lava); otherwise the plain getTickDelay. Sulfur's fluid path is deterministic (no
// per-cell RNG stream is pinned by the pig oracle), and the lava case here mirrors the jar's
// random.nextInt(4) draw on the region levelRandom. For water this is a pure getTickDelay(5).
// CITE: net.minecraft.world.level.material.LavaFluid.getSpreadDelay / FlowingFluid.getSpreadDelay.
func (t *TickLoop) getSpreadDelay(pos pk.Position, oldState, newState fluidState) int {
	delay := oldState.tickDelay()
	if !oldState.isLava {
		return delay // WaterFluid (and empty): no override, plain getTickDelay.
	}
	// LavaFluid.getSpreadDelay: both states non-empty, non-falling lava, and the NEW height strictly
	// greater than the OLD (lava rising) -> draw nextInt(4); if != 0, delay *= 4. CITE: bci 55-89
	// (aload4=new.getHeight fcmpl aload3=old.getHeight; ifle skips).
	if oldState.isFluid() && newState.isLava && newState.isFluid() &&
		!oldState.falling && !newState.falling &&
		newState.ownHeight() > oldState.ownHeight() {
		r := t.cur().levelRandom
		if r != nil && r.NextIntN(4) != 0 {
			delay *= 4
		}
	}
	return delay
}

func (t *TickLoop) scheduleEmpty() bool {
	return t.cur().fluidSchedule == nil || t.cur().fluidSchedule.empty()
}

// horizontalDirs is the 4 cardinal horizontal offsets (Direction.Plane.HORIZONTAL).
var horizontalDirs = [4]pk.Position{
	{X: 1}, {X: -1}, {Z: 1}, {Z: -1},
}

// below/above/relative are the BlockPos neighbor helpers (net.minecraft.core.BlockPos).
func below(p pk.Position) pk.Position { return pk.Position{X: p.X, Y: p.Y - 1, Z: p.Z} }
func above(p pk.Position) pk.Position { return pk.Position{X: p.X, Y: p.Y + 1, Z: p.Z} }
func plus(p, d pk.Position) pk.Position {
	return pk.Position{X: p.X + d.X, Y: p.Y + d.Y, Z: p.Z + d.Z}
}

// fluidAt decodes the fluid at a world position (empty for unloaded/non-water).
func (t *TickLoop) fluidAt(pos pk.Position) fluidState {
	id, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return fluidState{}
	}
	return decodeFluid(id)
}

// isSolidAt reports whether the block at pos is a solid (non-air, non-water) barrier. Fluid
// cannot pass through or replace a solid. This is the v1 passability subset of
// FlowingFluid.canPassThroughWall (the full VoxelShape face-occlusion is simplified to a
// solid/non-solid test for the water gate - 17-RESEARCH Pattern 5).
func (t *TickLoop) isSolidAt(pos pk.Position) bool {
	id, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded reads as non-solid (matches GetBlock's "no block here" contract)
	}
	if block.IsAir(id) {
		return false
	}
	if _, isWater := waterLevelOf(id); isWater {
		return false
	}
	// Lava, like water, does not block motion or fluid passage (BlockState.blocksMotion() is false
	// for both liquids). Treat a lava cell as non-solid so the fluid sim can pass/replace it and so
	// isSolidAt's other consumers (mob/projectile passability) see lava as open, matching vanilla.
	if _, isLava := lavaLevelOf(id); isLava {
		return false
	}
	return true
}

// fluidTick is the per-position FlowingFluid.tick body (net.minecraft.world.level.material.
// FlowingFluid.tick). For a non-source fluid: recompute getNewLiquid; if it differs from the
// current state, write it (AIR when empty) and re-schedule the affected cell. THEN always
// spread. A source never recomputes its own level (it stays full) but still spreads.
func (t *TickLoop) fluidTick(pos pk.Position) {
	cur := t.fluidAt(pos)
	if !cur.isFluid() {
		return // nothing to tick here (block changed since it was scheduled)
	}

	if !cur.source {
		next := t.getNewLiquid(pos)
		if !sameFluid(cur, next) {
			if !next.isFluid() {
				// FlowingFluid.tick empty branch: setBlock(pos, AIR, flag 3). Flag 3 =
				// UPDATE_NEIGHBORS|UPDATE_CLIENTS -> Level.updateNeighborsAt fires neighborChanged on
				// all 6 neighbours, and each neighbouring LiquidBlock.neighborChanged re-schedules its
				// own fluid tick (getTickDelay). scheduleNeighbors is that 6-neighbour wake, so the
				// drained cell's neighbours re-flow into/around the newly-empty cell.
				t.setFluidBlock(pos, airStateID())
				t.scheduleNeighbors(pos)
				return
			}
			// FlowingFluid.tick diminish branch: setBlock(pos, newLiquid.createLegacyBlock(), flag 3)
			// THEN scheduleTick(pos, fluid, spreadDelay). The flag-3 write ALSO fires
			// Level.updateNeighborsAt -> neighborChanged on all 6 neighbours (identical to the empty
			// branch above - vanilla makes NO distinction, both writes use flag 3). Sulfur previously
			// rescheduled ONLY this cell here, so a diminishing cell never woke its downstream
			// neighbours: when a source was removed, the innermost ring re-evaluated once but the
			// outer rings were never re-scheduled and stayed frozen at their stale level forever (the
			// "flowing water does not drain" bug). scheduleNeighbors restores the flag-3 6-neighbour
			// wake so the diminish cascades outward to the fixed point (each ring drops by dropOff per
			// pass until every flowing cell reaches amount 0 -> air). CITE: FlowingFluid.tick ->
			// ServerLevel.setBlock(...,3) -> LiquidBlock.neighborChanged -> scheduleTick.
			t.setFluidBlock(pos, encodeFluid(next))
			// scheduleTick(pos, newLiquid.getType(), getSpreadDelay(level, pos, oldState=cur, newState=next)).
			// LavaFluid.getSpreadDelay returns a SHORTER delay (tickDelay, else tickDelay*4 when the
			// existing lava is higher) when both states are non-empty non-falling lava; water always
			// returns getTickDelay. CITE: FlowingFluid.tick -> getSpreadDelay.
			t.scheduleFluidTickDelay(pos, next, t.getSpreadDelay(pos, cur, next))
			t.scheduleNeighbors(pos)
			cur = next
		}
	}

	// LiquidBlock.shouldSpreadLiquid gate (LAVA only): before a lava cell flows, vanilla checks its
	// neighbours for water and, if found, SOLIDIFIES this cell (obsidian if source, else cobblestone)
	// instead of spreading. shouldSpreadLiquid does the replacement + fizz and returns false; the
	// scheduled spread is then skipped. Water always returns true (no-op) so its flow is unperturbed.
	// Cite: net.minecraft.world.level.block.LiquidBlock.shouldSpreadLiquid (called from onPlace/
	// neighborChanged/the scheduleTick gate; here folded into the per-cell tick before spread()).
	if !t.shouldSpreadLiquid(pos, cur) {
		return
	}

	t.spread(pos, cur)
}

// setFluidBlock is the fluid sim's sole world-mutation primitive: it writes the new state AND
// broadcasts a ClientboundBlockUpdate to every player watching the column. Vanilla's
// Level.setBlock(pos, state, UPDATE_CLIENTS) does both - the fluid tick mutated the server world
// but the CLIENT never saw it, so water that flowed (filled a gap, drained, changed level) was
// invisible: the client kept rendering the chunk's load-time state. This made the fluid sim look
// inert from the client even though the server was flowing correctly (and made flowing/falling
// water never animate). Routing every fluid write through here keeps server and client in sync,
// exactly as a break/place edit does via reconcileEdit→broadcastBlockUpdate. Cite:
// net.minecraft.world.level.Level.setBlock with Block.UPDATE_CLIENTS. Returns whether the write
// changed the cell (mirrors world.SetBlock) so callers can gate on a real change.
func (t *TickLoop) setFluidBlock(pos pk.Position, state block.StateID) bool {
	if !t.world().SetBlock(pos, state, dimMinY) {
		return false // no change (already this state / unloaded): nothing to broadcast
	}
	t.broadcastBlockUpdate(pos, state)
	return true
}

// sameFluid reports whether two fluid states encode the same wire block (so no write/reschedule
// is needed). Air-vs-air and identical water states are "same".
func sameFluid(a, b fluidState) bool {
	if !a.sameKind(b) {
		return false
	}
	if !a.isFluid() {
		return true // both non-fluid (air-vs-air)
	}
	return getLegacyLevel(a.amount, a.falling, a.source) == getLegacyLevel(b.amount, b.falling, b.source)
}

// getNewLiquid is the heart of the flow: it examines the 4 horizontal neighbors + the block
// above and computes the fluid state this cell SHOULD hold. PORT of
// FlowingFluid.getNewLiquid (verified bytecode):
//
//  1. for each same-fluid horizontal neighbor that can pass through: track the max amount and
//     count sources.
//  2. if sourceCount >= 2 AND canConvertToSource AND (block below is solid OR a source of this
//     type) -> become a source.
//  3. if the same fluid is directly above and can pass down -> falling, full (amount 8).
//  4. else amount = maxAmount - dropOff; if <= 0 -> empty; else flowing at that amount.
func (t *TickLoop) getNewLiquid(pos pk.Position) fluidState {
	// Determine which fluid this cell is being recomputed FOR. FlowingFluid.getNewLiquid runs on a
	// specific fluid instance (`this`) and only counts neighbors whose type == this. When the cell
	// already holds a fluid, that fixes the kind; when it is air (a cell being flowed INTO), the
	// kind is the fluid of the neighbors driving the flow - we take the fluid directly above, else
	// the first same-fluid horizontal neighbor, matching vanilla's "this is the flowing fluid".
	cur := t.fluidAt(pos)
	if !cur.isFluid() {
		if af := t.fluidAt(above(pos)); af.isFluid() {
			cur = af.makeFluid(0, false, false)
		} else {
			for _, d := range horizontalDirs {
				if nf := t.fluidAt(plus(pos, d)); nf.isFluid() {
					cur = nf.makeFluid(0, false, false)
					break
				}
			}
		}
		if !cur.isFluid() {
			return fluidState{} // no fluid anywhere adjacent to recompute
		}
	}
	maxAmount := 0
	sourceCount := 0
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		nf := t.fluidAt(np)
		if !nf.sameKind(cur) || !nf.isFluid() {
			continue // only the SAME fluid contributes (FlowingFluid.getNewLiquid: isSame check)
		}
		// canPassThroughWall(dir, pos, np): a solid slab/stair face between the two cells stops the
		// contribution (VoxelShape face-occlusion). CITE: getNewLiquid bci 85-97.
		if !t.canPassThroughWall(dirHoriz, pos, np) {
			continue
		}
		if nf.source {
			sourceCount++
		}
		if nf.amount > maxAmount {
			maxAmount = nf.amount
		}
	}

	// Source conversion (FlowingFluid.getNewLiquid >= 2 sources branch). Gated on the fluid's OWN
	// canConvertToSource (water default true; lava default false - LavaFluid never converts here).
	if sourceCount >= 2 && cur.sourceConversion() {
		belowPos := below(pos)
		belowSolid := t.isSolidAt(belowPos)
		belowFluid := t.fluidAt(belowPos)
		if belowSolid || (belowFluid.sameKind(cur) && belowFluid.source) {
			return cur.makeFluid(waterSourceAmount, false, true)
		}
	}

	// Falling rule: same fluid directly above + can pass down -> falling, full.
	abovePos := above(pos)
	af := t.fluidAt(abovePos)
	// getNewLiquid UP branch (bci 185-254): same fluid directly above that canPassThroughWall(UP)
	// -> falling, full. CITE: FlowingFluid.getNewLiquid.
	if af.sameKind(cur) && af.isFluid() && t.canPassThroughWall(dirUp, pos, abovePos) {
		return cur.makeFluid(waterSourceAmount, true, false)
	}

	newAmount := maxAmount - cur.dropOff()
	if newAmount <= 0 {
		return fluidState{} // empty
	}
	return cur.makeFluid(newAmount, false, false)
}

// spread propagates the fluid at pos. PORT of FlowingFluid.spread (verified bytecode):
//
//  1. if empty -> return.
//  2. look at the cell below: if it can be passed through, compute its new liquid; if the
//     below cell can be replaced + hold the fluid -> spreadTo DOWN (a falling column). If this
//     cell has >= 3 source neighbors, ALSO spreadToSides.
//  3. otherwise (down is blocked): if this fluid is a source OR the below cell is not a "water
//     hole" (cannot drain further) -> spreadToSides.
func (t *TickLoop) spread(pos pk.Position, f fluidState) {
	if !f.isFluid() {
		return
	}
	belowPos := below(pos)
	// DOWN branch (FlowingFluid.spread): the below cell must (a) canMaybePassThrough DOWN (not a
	// source of this type, canHoldAnyFluid, AND canPassThroughWall through the DOWN face), (b) its
	// current fluid must canBeReplacedWith(getNewLiquid(below).getType(), DOWN), and (c) it must be
	// able to hold that specific fluid. Then spreadTo DOWN with the below cell's OWN getNewLiquid.
	if t.canMaybePassThrough(pos, belowPos, dirDown, f) {
		downLiquid := t.getNewLiquid(belowPos)
		if canBeReplacedWith(t.fluidAt(belowPos), downLiquid, dirDown) && t.canHoldSpecificFluidAt(belowPos, downLiquid) {
			t.spreadToDir(belowPos, dirDown, downLiquid)
			if t.sourceNeighborCount(pos, f) >= 3 {
				t.spreadToSides(pos, f)
			}
			return
		}
	}
	// Down is blocked. Spread sideways unless the below cell is a drainable hole that a
	// non-source flow should pour into instead of spreading (isWaterHole).
	if f.source || !t.isHole(belowPos, f) {
		t.spreadToSides(pos, f)
	}
}

// canMaybePassThrough ports FlowingFluid.canMaybePassThrough(level, fromPos, fromState, dir, toPos,
// toState, toFluid): the cell is passable for the flow iff it is NOT a source of this type,
// canHoldAnyFluid(toState) is true, AND canPassThroughWall(dir, from, to) (the VoxelShape
// face-occlusion). This is the shared pass-through gate used by spread(DOWN), getSpread, and isHole.
// CITE: net.minecraft.world.level.material.FlowingFluid.canMaybePassThrough.
func (t *TickLoop) canMaybePassThrough(from, to pk.Position, dir fluidDir, f fluidState) bool {
	toFluid := t.fluidAt(to)
	if toFluid.sameKind(f) && toFluid.isFluid() && toFluid.source {
		return false // isSourceBlockOfThisType(toFluid) -> a source of this type is never passed through
	}
	if !t.canHoldAnyFluidAt(to) {
		return false // canHoldAnyFluid(toState)
	}
	return t.canPassThroughWall(dir, from, to)
}

// canPassThroughWall ports FlowingFluid.canPassThroughWall(dir, level, fromPos, fromState, toPos,
// toState): the fluid can pass from `from` into `to` across the `dir` face unless a solid wall on
// EITHER side occludes that face. Vanilla: if either collision shape is the full block cube -> false;
// otherwise true iff !Shapes.mergedFaceOccludes(fromShape, toShape, dir). Sulfur has the exact
// per-state face-occlusion masks baked (block.ShapeOccludes == Shapes.faceShapeOccludes on the
// occlusion shapes), so the merged-face test is ShapeOccludes(from, to, dir) and the full-cube early
// returns are IsCollisionShapeFullBlock. (DEBUG_DISABLE_LIQUID_SPREADING / half-world debug flags are
// false in a normal server.) CITE: net.minecraft.world.level.material.FlowingFluid.canPassThroughWall.
func (t *TickLoop) canPassThroughWall(dir fluidDir, from, to pk.Position) bool {
	fromID, ok := t.world().GetBlock(from, dimMinY)
	if !ok {
		return false
	}
	toID, ok := t.world().GetBlock(to, dimMinY)
	if !ok {
		return false
	}
	// if getCollisionShape(from)==Shapes.block() -> false; same for to.
	if block.IsCollisionShapeFullBlock(fromID) || block.IsCollisionShapeFullBlock(toID) {
		return false
	}
	// return !mergedFaceOccludes(toShape, fromShape, dir) - the face between the two cells is NOT
	// fully occluded by the union of the two touching faces.
	return !block.ShapeOccludes(fromID, toID, fluidDirToBlockDir(dir))
}

// canHoldSpecificFluidAt is FlowingFluid.canHoldSpecificFluid(level, pos, state, fluid) for the v1
// container-less world: a non-container block holds a specific fluid iff it canHoldAnyFluid (the
// LiquidBlockContainer.canPlaceLiquid path is absent until waterlogging exists). CITE:
// FlowingFluid.canHoldSpecificFluid.
func (t *TickLoop) canHoldSpecificFluidAt(pos pk.Position, f fluidState) bool {
	return f.isFluid() && t.canHoldAnyFluidAt(pos)
}

// fluidDirToBlockDir maps the fluid flow direction to the block.Direction used by ShapeOccludes.
func fluidDirToBlockDir(d fluidDir) block.Direction {
	switch d {
	case dirDown:
		return block.Down
	case dirUp:
		return block.Up
	default:
		return block.North // any horizontal face; ShapeOccludes is symmetric across horizontals for full/empty faces
	}
}

// canHoldAnyFluidAt reads the block at pos and reports block.CanHoldAnyFluid (the 1:1
// FlowingFluid.canHoldAnyFluid replaceability gate). An unloaded read is NOT replaceable (the fluid
// must not flow into a chunk that is not present); a fluid write to an unloaded column is a no-op
// anyway. Air, water, and lava are all replaceable (IsAir -> not solid, no exception; liquids carry
// no Waterlogged field and do not blocksMotion), so this is a strict superset of the old air/fluid
// test PLUS replaceable non-solids.
func (t *TickLoop) canHoldAnyFluidAt(pos pk.Position) bool {
	id, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded: do not spread into an absent column
	}
	return block.CanHoldAnyFluid(id)
}

// spreadToSides flows to the horizontal neighbor(s) biased toward the nearest drop-off. PORT of
// FlowingFluid.spreadToSides + getSpread + getSlopeDistance (verified bytecode): the outgoing
// amount is amount-dropOff (or 7 when falling); getSpread keeps only the directions with the
// minimum slope distance to a hole within getSlopeFindDistance(=4).
func (t *TickLoop) spreadToSides(pos pk.Position, f fluidState) {
	// spreadToSides gate (FlowingFluid.spreadToSides): amount - getDropOff, or 7 when falling; if <= 0
	// nothing spreads. The gate remains, but the per-direction FluidState now comes from getSpread's
	// PER-NEIGHBOR getNewLiquid (each direction carries its own computed state), not a uniform amount.
	outAmount := f.amount - f.dropOff()
	if f.falling {
		outAmount = waterSourceAmount - f.dropOff()
	}
	if outAmount <= 0 {
		return
	}
	for _, e := range t.getSpread(pos, f) {
		np := plus(pos, e.dir)
		t.spreadToDir(np, dirHoriz, e.state)
	}
}

// getSpread returns the horizontal directions to spread into, biased toward the nearest
// drop-off via the slope-find. PORT of FlowingFluid.getSpread: for each passable horizontal
// neighbor that can hold fluid, compute its slope distance (0 if a hole sits below it); keep
// only the directions tied for the minimum distance.
func (t *TickLoop) getSpread(pos pk.Position, f fluidState) []spreadEntry {
	minSlope := 1000
	var out []spreadEntry
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		// canMaybePassThrough(pos, np, HORIZONTAL, f): source-of-type / canHoldAnyFluid / wall.
		if !t.canMaybePassThrough(pos, np, dirHoriz, f) {
			continue
		}
		// getNewLiquid(np) - the neighbor computes ITS OWN new fluid state (this is the per-direction
		// state the jar carries; the old code wrote one uniform amount to every direction).
		newLiquid := t.getNewLiquid(np)
		if !t.canHoldSpecificFluidAt(np, newLiquid) {
			continue
		}
		var slope int
		if t.isHole(np, f) {
			slope = 0
		} else {
			slope = t.getSlopeDistance(np, 1, opposite(d), f)
		}
		if slope < minSlope {
			minSlope = slope
			out = out[:0]
		}
		if slope <= minSlope {
			// put(dir, newLiquid) gated on the neighbor's current fluid canBeReplacedWith the new
			// liquid in this HORIZONTAL direction (FluidState.canBeReplacedWith at bci 192-216).
			if canBeReplacedWith(t.fluidAt(np), newLiquid, dirHoriz) {
				out = append(out, spreadEntry{dir: d, state: newLiquid})
			}
			minSlope = slope
		}
	}
	return out
}

// spreadEntry is one (Direction -> computed FluidState) pair of the getSpread EnumMap result.
type spreadEntry struct {
	dir   pk.Position
	state fluidState
}

// getSlopeDistance is the recursive 8-direction slope-find within getSlopeFindDistance(=4). PORT
// of FlowingFluid.getSlopeDistance: from a cell at distance i, examine each horizontal neighbor
// (except the one we came from); if it can be passed through and is a hole (can flow down),
// return i; otherwise, if i < slopeFindDistance, recurse with i+1. Returns the minimum distance
// to a drop-off, or 1000 if none within range.
func (t *TickLoop) getSlopeDistance(pos pk.Position, dist int, excludeDir pk.Position, f fluidState) int {
	best := 1000
	for _, d := range horizontalDirs {
		if d == excludeDir {
			continue
		}
		np := plus(pos, d)
		if !t.canMaybePassThrough(pos, np, dirHoriz, f) {
			continue
		}
		if t.isHole(np, f) {
			return dist
		}
		if dist < f.slopeFindDistance() {
			if r := t.getSlopeDistance(np, dist+1, opposite(d), f); r < best {
				best = r
			}
		}
	}
	return best
}

// isHole reports whether fluid would drain downward THROUGH pos - pos itself must be passable (a
// solid cell is never a hole) AND the cell below pos can hold fluid. PORT of
// FlowingFluid.isWaterHole / SpreadContext.isHole (v1 subset). A solid floor cell is NOT a hole,
// so flow over a flat floor spreads sideways (it cannot fall through the floor).
func (t *TickLoop) isHole(pos pk.Position, f fluidState) bool {
	// SpreadContext.isHole(pos): the cell below `pos` can be flowed into DOWN. pos itself must first
	// be reachable (a solid pos is never a hole - guarded by getSpread/getSlopeDistance's
	// canMaybePassThrough before this is called). CITE: FlowingFluid$SpreadContext.isHole ->
	// canPassThroughWall/canMaybePassThrough(pos.below(), DOWN).
	return t.canMaybePassThrough(pos, below(pos), dirDown, f)
}

// sourceNeighborCount counts the horizontal neighbors that are source blocks of this fluid.
// PORT of FlowingFluid.sourceNeighborCount.
func (t *TickLoop) sourceNeighborCount(pos pk.Position, f fluidState) int {
	n := 0
	for _, d := range horizontalDirs {
		nf := t.fluidAt(plus(pos, d))
		if nf.sameKind(f) && nf.isFluid() && nf.source {
			n++
		}
	}
	return n
}

// spreadTo writes a fluid into a neighbor cell and schedules it to tick (so the flow continues
// on the getTickDelay-spaced queue). PORT of FlowingFluid.spreadTo (verified bytecode): if the
// target is a LiquidBlockContainer -> placeLiquid (waterlog); else if it is not air ->
// beforeDestroyingBlock (dropResources) then setBlock(fluid). Sulfur has no waterlog sim yet, so a
// container is stopped by the canHoldAnyFluid gate (kept as-is, per scope); a REPLACEABLE non-solid
// is dropped + overwritten. Only writes when the target actually changes, so the queue reaches a
// fixed point (termination).
func (t *TickLoop) spreadToDir(pos pk.Position, dir fluidDir, f fluidState) {
	brokenState, loaded := t.world().GetBlock(pos, dimMinY)
	if !loaded || !block.CanHoldAnyFluid(brokenState) {
		return // unloaded, or a solid/container/exception block that STOPS the fluid
	}
	cur := t.fluidAt(pos)

	// LavaFluid.spreadTo override: lava spreading DOWNWARD onto a WATER cell turns that cell to
	// STONE and fizzes (steam particles + sound). The DOWN direction is the one where the target
	// cell already holds water and f is lava; the horizontal lava-meets-water obsidian/cobblestone
	// (LiquidBlock.shouldSpreadLiquid) is a separate neighbor-driven path, DEFERRED here.
	// javap net.minecraft.world.level.material.LavaFluid.spreadTo:
	//   if dir==DOWN && this.is(FluidTags.LAVA) && belowFluid.is(FluidTags.WATER):
	//       if state instanceof LiquidBlock: setBlock(pos, STONE, 3); fizz(level,pos); return
	// We detect the DOWN case as "f is lava and the target already holds water" (spread() only
	// reaches a water cell below via the down branch). No stone is set for the empty-air case.
	if f.isLava && cur.isWater {
		t.setFluidBlock(pos, lavaSolidifyState())
		t.fizz(pos)
		return
	}
	if cur.isFluid() && !cur.sameKind(f) {
		// spreadTo only writes a foreign fluid via the lava-DOWN-onto-water -> stone branch handled
		// above. If that did not fire, a foreign cell is not overwritten by this fluid's flow.
		if !canBeReplacedWith(cur, f, dir) {
			return
		}
	}
	if sameFluid(cur, f) {
		return // no change: do not re-schedule (prevents infinite oscillation)
	}
	// beforeDestroyingBlock (FlowingFluid.spreadTo): when the target holds a block that is NOT air
	// and NOT already this fluid's flow (i.e. a REPLACEABLE non-solid the fluid is about to destroy -
	// tall grass, flowers, torches, redstone, ...), drop its resources BEFORE overwriting it with the
	// flowing fluid. WaterFluid.beforeDestroyingBlock == Block.dropResources(state, level, pos, be);
	// spawnBlockDrop(nil, ...) is the drop-with-no-breaker analogue (the entity arg is null, exactly
	// as vanilla passes for a fluid-driven destroy). A cell already holding a fluid takes no drop (it
	// is replaced, not destroyed). CITE: FlowingFluid.spreadTo -> beforeDestroyingBlock; WaterFluid.
	// beforeDestroyingBlock.
	if !cur.isFluid() && !block.IsAir(brokenState) {
		t.spawnBlockDrop(nil, pos, brokenState)
	}
	if t.setFluidBlock(pos, encodeFluid(f)) {
		udebug("fluid", "spreadTo (%d,%d,%d) kind=%s amount=%d falling=%v source=%v", pos.X, pos.Y, pos.Z, fluidKindName(f), f.amount, f.falling, f.source)
		t.scheduleFluidTickKind(pos, f)
	}
}

// lavaFireSpreadRange is LavaFluid.randomTick's offset range: nextInt(3)-1 in each axis => [-1,1].
const lavaFireSpreadRange = 3

// lavaRandomTick ports net.minecraft.world.level.material.LavaFluid.randomTick(level, pos, state,
// random) - lava's ambient FIRE SPREAD (LavaFluid.isRandomlyTicking() == true). It is invoked by the
// random-tick driver (server/random_tick.go dispatchRandomTick, whose `case block.IsLava` +
// level/block.IsRandomlyTicking(Lava)->true are the one-line integration seam owned by those files;
// this method is the whole behavior). Verified bytecode (26.2 jar):
//
//	if (!canSpreadFireAround(pos)) return;                       // v1: always true (fire_block.go note)
//	i = random.nextInt(3);
//	if (i > 0) {                                                 // above-air branch
//	  m = pos;
//	  for (j = 0; j < i; j++) {
//	    m = m.offset(random.nextInt(3)-1, 1, random.nextInt(3)-1);
//	    if (!isLoaded(m)) return;
//	    bs = getBlockState(m);
//	    if (bs.isAir()) { if (hasFlammableNeighbours(m)) { setBlockAndUpdate(m, BaseFireBlock.getState(m)); return; } }
//	    else if (bs.blocksMotion()) return;
//	  }
//	} else {                                                     // side-ignite branch
//	  for (k = 0; k < 3; k++) {
//	    m = pos.offset(random.nextInt(3)-1, 0, random.nextInt(3)-1);
//	    if (!isLoaded(m)) return;
//	    if (isEmptyBlock(m.above()) && isFlammable(m)) setBlockAndUpdate(m.above(), BaseFireBlock.getState(m.above()));
//	  }
//	}
//
// RNG DRAW ORDER is mirrored EXACTLY on r.levelRandom (nextInt(3) offsets), so lava fire spread is in
// lockstep with vanilla. isFlammable(pos) == isInsideBuildHeight && hasChunkAt && state.ignitedByLava();
// Sulfur has no per-state ignitedByLava flag baked yet, so the flammable read uses the fire ignite
// table (fireIgniteOdds>0) as the cited stand-in - the overworld flammable set (planks/logs/leaves/
// wool/plants/...) that setFlammable populates is the SAME set Properties.ignitedByLava() marks, so
// the observable ignition behavior matches. CITE: LavaFluid.randomTick / .hasFlammableNeighbours /
// .isFlammable; BaseFireBlock.getState.
func (t *TickLoop) lavaRandomTick(r *region, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	// canSpreadFireAround(pos): v1 always true (gamerule fire_spread_radius default -1). Same cited
	// constant as fire_block.go / lightning.go.
	i := int(r.levelRandom.NextIntN(lavaFireSpreadRange))
	if i > 0 {
		m := pos
		for j := 0; j < i; j++ {
			dx := int(r.levelRandom.NextIntN(lavaFireSpreadRange)) - 1
			dz := int(r.levelRandom.NextIntN(lavaFireSpreadRange)) - 1
			m = pk.Position{X: m.X + dx, Y: m.Y + 1, Z: m.Z + dz}
			st, ok := t.world().GetBlock(m, dimMinY)
			if !ok {
				return // !isLoaded(m) -> return
			}
			if block.IsAir(st) {
				if t.lavaHasFlammableNeighbours(m) {
					if fs, ok := t.fireStateForPlacement(m); ok {
						if t.world().SetBlock(m, fs, dimMinY) {
							t.broadcastBlockUpdate(m, fs)
						}
					}
					return
				}
			} else if t.isSolidAt(m) {
				// else if (state.blocksMotion()) return; - isSolidAt is fluid.go's v1 blocksMotion
				// subset (air/water/lava are non-blocking; any other block blocks). CITE: LavaFluid.
				// randomTick blocksMotion guard.
				return
			}
		}
		return
	}
	for k := 0; k < 3; k++ {
		sdx := int(r.levelRandom.NextIntN(lavaFireSpreadRange)) - 1
		sdz := int(r.levelRandom.NextIntN(lavaFireSpreadRange)) - 1
		m := pk.Position{X: pos.X + sdx, Y: pos.Y, Z: pos.Z + sdz}
		if _, ok := t.world().GetBlock(m, dimMinY); !ok {
			return // if (!isLoaded(m)) return; - aborts the whole method (matches vanilla)
		}
		// isEmptyBlock(m.above()) && isFlammable(m) -> ignite the cell above m.
		aboveM := above(m)
		aboveSt, aok := t.world().GetBlock(aboveM, dimMinY)
		if aok && block.IsAir(aboveSt) && t.lavaIsFlammable(m) {
			if fs, ok := t.fireStateForPlacement(aboveM); ok {
				if t.world().SetBlock(aboveM, fs, dimMinY) {
					t.broadcastBlockUpdate(aboveM, fs)
				}
			}
		}
	}
}

// lavaHasFlammableNeighbours ports LavaFluid.hasFlammableNeighbours: true iff any of the 6
// Direction.values() neighbours of pos isFlammable. CITE: LavaFluid.hasFlammableNeighbours.
func (t *TickLoop) lavaHasFlammableNeighbours(pos pk.Position) bool {
	for _, o := range fireNeighborOffsets { // Direction.values() order: DOWN,UP,NORTH,SOUTH,WEST,EAST
		np := pk.Position{X: pos.X + o.dx, Y: pos.Y + o.dy, Z: pos.Z + o.dz}
		if t.lavaIsFlammable(np) {
			return true
		}
	}
	return false
}

// lavaIsFlammable ports LavaFluid.isFlammable(level, pos): inside build height && chunk present &&
// state.ignitedByLava(). ignitedByLava() is stood in by fireIgniteOdds>0 (see lavaRandomTick note).
// The isInsideBuildHeight / hasChunkAt guards are satisfied by GetBlock returning ok. CITE:
// LavaFluid.isFlammable.
func (t *TickLoop) lavaIsFlammable(pos pk.Position) bool {
	st, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	return t.fireIgniteOdds(st) > 0
}

// lavaSolidifyState is the lava-meets-water solidification target for the VERTICAL case
// (LavaFluid.spreadTo DOWN override): lava flowing DOWN onto water -> Blocks.STONE default state.
func lavaSolidifyState() block.StateID {
	return block.ToStateID[block.Stone{}]
}

// obsidianState / cobblestoneState / basaltState are the HORIZONTAL solidification targets
// (LiquidBlock.shouldSpreadLiquid): a SOURCE lava beside water -> Blocks.OBSIDIAN, a FLOWING lava
// beside water -> Blocks.COBBLESTONE, and (nether) lava over soul_soil beside blue_ice ->
// Blocks.BASALT. All use the block's defaultBlockState(): obsidian/cobblestone are propertyless;
// basalt's RotatedPillarBlock default sets AXIS=Y (javap RotatedPillarBlock.<init>), so we pin
// Axis:Y rather than the Go zero value (X). Cite Blocks.OBSIDIAN / .COBBLESTONE / .BASALT.
func obsidianState() block.StateID    { return block.ToStateID[block.Obsidian{}] }
func cobblestoneState() block.StateID { return block.ToStateID[block.Cobblestone{}] }
func basaltState() block.StateID      { return block.ToStateID[block.Basalt{Axis: block.Y}] }

// isBlockAt reports whether the world block at pos is exactly the given (default-state) block id.
// Used by shouldSpreadLiquid for the soul_soil (below) and blue_ice (neighbour) checks
// (BlockState.is(Block)).
func (t *TickLoop) isBlockAt(pos pk.Position, id block.StateID) bool {
	cur, ok := t.world().GetBlock(pos, dimMinY)
	return ok && cur == id
}

// shouldSpreadLiquid ports net.minecraft.world.level.block.LiquidBlock.shouldSpreadLiquid - the
// horizontal lava/water solidification gate. It runs ONLY for lava (water always returns true, so
// water flow is unperturbed) and returns false when the cell solidified (the caller then skips the
// spread), true when the cell should flow normally. Verified bytecode (26.2 jar):
//
//	if this.fluid.is(FluidTags.LAVA):
//	    flowsIntoSoulSoil = level.getBlockState(pos.below()).is(SOUL_SOIL)
//	    for d in POSSIBLE_FLOW_DIRECTIONS = [DOWN, SOUTH, NORTH, EAST, WEST]:
//	        neighbor = pos.relative(d.getOpposite())          // = [UP, NORTH, SOUTH, WEST, EAST]
//	        if level.getFluidState(neighbor).is(FluidTags.WATER):
//	            block = level.getFluidState(pos).isSource() ? OBSIDIAN : COBBLESTONE
//	            level.setBlockAndUpdate(pos, block.defaultBlockState())
//	            fizz(level, pos); return false
//	        if flowsIntoSoulSoil && level.getBlockState(neighbor).is(BLUE_ICE):
//	            level.setBlockAndUpdate(pos, BASALT.defaultBlockState())
//	            fizz(level, pos); return false
//	return true
//
// So the checked neighbours are UP + the four horizontals (NOT below); the source-vs-flowing test
// reads the fluidstate AT pos (the lava cell) - a SOURCE lava produces OBSIDIAN, a FLOWING lava
// produces COBBLESTONE. The soul_soil/blue_ice -> BASALT branch is the nether interaction, ported
// 1:1 (harmless in the overworld where blue_ice/soul_soil rarely coincide).
func (t *TickLoop) shouldSpreadLiquid(pos pk.Position, f fluidState) bool {
	if !f.isLava {
		return true // LiquidBlock.shouldSpreadLiquid is a no-op for non-lava (water)
	}
	flowsIntoSoulSoil := t.isBlockAt(below(pos), block.ToStateID[block.SoulSoil{}])
	// POSSIBLE_FLOW_DIRECTIONS opposites: DOWN->UP, SOUTH->NORTH, NORTH->SOUTH, EAST->WEST, WEST->EAST.
	// Iterate in the SAME order as the jar's ImmutableList so the first water-adjacent neighbour
	// (and therefore which solidification fires) is deterministic and matches vanilla.
	neighbors := [5]pk.Position{
		above(pos),                         // DOWN.getOpposite() = UP
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, // SOUTH.getOpposite() = NORTH
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, // NORTH.getOpposite() = SOUTH
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, // EAST.getOpposite()  = WEST
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, // WEST.getOpposite()  = EAST
	}
	for _, np := range neighbors {
		if t.fluidAt(np).isWater {
			// source lava -> obsidian, flowing lava -> cobblestone (fluidstate AT the lava cell).
			solid := cobblestoneState()
			if t.fluidAt(pos).source {
				solid = obsidianState()
			}
			t.setFluidBlock(pos, solid)
			t.fizz(pos)
			return false
		}
		if flowsIntoSoulSoil && t.isBlockAt(np, block.ToStateID[block.BlueIce{}]) {
			t.setFluidBlock(pos, basaltState())
			t.fizz(pos)
			return false
		}
	}
	return true
}

// lavaExtinguishEvent is the levelEvent id vanilla fires when lava solidifies against water
// (LiquidBlock.fizz / LavaFluid.spreadTo -> level.levelEvent(1501, pos, 0)). On the client, case
// 1501 resolves to the LAVA_EXTINGUISH sound + a burst of 8 LARGE_SMOKE particles at random
// offsets. Cite: net.minecraft.world.level.block.LiquidBlock.fizz.
const lavaExtinguishEvent = 1501

// fizz ports net.minecraft.world.level.block.LiquidBlock.fizz -> level.levelEvent(1501, pos, 0):
// the extinguish sound + smoke burst emitted when lava turns to stone/obsidian/cobblestone/basalt
// against water (and reused for the vertical LavaFluid.spreadTo->stone case). Sulfur has no
// ClientboundLevelEvent broadcast plumbing in the fluid path yet, so the client-facing sound +
// particle burst is DEFERRED (documented seam); the block replacement is done by the caller and the
// levelEvent is surfaced through fizzHook so tests can assert the fizzle fired at the right cell.
func (t *TickLoop) fizz(pos pk.Position) {
	udebug("fluid", "lava+water fizz (levelEvent %d) at (%d,%d,%d)", lavaExtinguishEvent, pos.X, pos.Y, pos.Z)
	if t.fizzHook != nil {
		t.fizzHook(pos, lavaExtinguishEvent)
	}
}

// fluidKindName is a debug label for the fluid kind.
func fluidKindName(f fluidState) string {
	switch {
	case f.isWater:
		return "water"
	case f.isLava:
		return "lava"
	default:
		return "none"
	}
}

// fluidDir is the flow Direction threaded through spread/spreadTo/getSpread/isHole/canBeReplacedWith.
// Only DOWN and the 4 horizontals ever reach canBeReplacedWith in the flow, plus UP inside
// getNewLiquid's fluid-above branch. Mirrors net.minecraft.core.Direction for the fluid subset.
type fluidDir int

const (
	dirDown fluidDir = iota
	dirUp
	dirHoriz // any horizontal (canBeReplacedWith only distinguishes DOWN from not-DOWN)
)

// canBeReplacedWith is the DIRECTION-DISPATCHED replaceability predicate - the 1:1 port of
// FluidState.canBeReplacedWith -> Fluid.canBeReplacedWith, which dispatches to the concrete fluid's
// override. There is NO amount comparison in vanilla; replaceability is a per-fluid-type function of
// the FLOW DIRECTION and the incoming fluid type:
//
//	EmptyFluid.canBeReplacedWith                  -> true   (air is always replaceable)
//	WaterFluid.canBeReplacedWith(state,...,dir)   -> dir == DOWN && !incoming.is(FluidTags.WATER)
//	LavaFluid.canBeReplacedWith(state,lvl,pos,incoming,dir)
//	                                              -> state.getHeight() >= 0.44444445f && incoming.is(WATER)
//
// So water-in-a-cell is replaceable ONLY by a non-water fluid flowing DOWN into it (lava pouring
// down onto water); a horizontal water flow NEVER replaces existing water (the old amount-comparison
// model was wrong). Lava-in-a-cell is replaceable ONLY by WATER when the lava is tall enough
// (height >= 4/9). CITE: net.minecraft.world.level.material.WaterFluid.canBeReplacedWith /
// LavaFluid.canBeReplacedWith / EmptyFluid.canBeReplacedWith / FluidState.canBeReplacedWith.
func canBeReplacedWith(cur, incoming fluidState, dir fluidDir) bool {
	if !cur.isFluid() {
		return true // EmptyFluid.canBeReplacedWith -> true
	}
	if cur.isWater {
		// WaterFluid: dir == DOWN && !incoming.is(WATER).
		return dir == dirDown && !incoming.isWater
	}
	if cur.isLava {
		// LavaFluid: getHeight() >= 0.44444445f && incoming.is(WATER).
		return cur.ownHeight() >= 0.44444445 && incoming.isWater
	}
	return false
}

// scheduleNeighbors schedules the 4 horizontal neighbors, the cell above, AND the cell below to
// re-evaluate - the port of the flag-3 setBlock inside FlowingFluid.tick, whose UPDATE_NEIGHBORS
// bit runs Level.updateNeighborsAt -> LiquidBlock.neighborChanged (-> scheduleTick) on ALL SIX
// neighbours of the mutated cell. It wakes each same-fluid neighbour so a drained/diminished cell
// propagates its change outward (down-column drains, sideways rings recede). The below neighbour
// matters for a draining falling column: the cell under a cell that just went to air/less must
// re-evaluate (its sustaining fluid above changed). CITE: Level.updateNeighborsAt (all 6) ->
// LiquidBlock.neighborChanged.
func (t *TickLoop) scheduleNeighbors(pos pk.Position) {
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		if nf := t.fluidAt(np); nf.isFluid() {
			t.scheduleFluidTickKind(np, nf)
		}
	}
	if af := t.fluidAt(above(pos)); af.isFluid() {
		t.scheduleFluidTickKind(above(pos), af)
	}
	if bf := t.fluidAt(below(pos)); bf.isFluid() {
		t.scheduleFluidTickKind(below(pos), bf)
	}
}

// scheduleFluidNeighborsOnEdit is the port of Level.updateNeighborsAt → LiquidBlock.neighborChanged
// for a block EDIT (break or place). Vanilla: Level.setBlock notifies all 6 neighbors of the
// changed position; each neighbor that is a LiquidBlock runs neighborChanged, and if
// shouldSpreadLiquid it re-schedules its fluid tick (FlowingFluid.getTickDelay ticks out). Sulfur's
// break/place path (reconcileEdit) mutated the world but never ran this neighbor notification, so a
// block broken next to (or below) standing water left a permanent air gap - the adjacent water was
// never re-scheduled and so never flowed into the new hole. Schedule the fluid in EACH of the 6
// neighbors (the 4 horizontals + above + below); a scheduled water cell re-runs FlowingFluid.tick,
// which spreads down into / sideways toward the freshly-opened air. The edited cell itself is also
// scheduled if it is now water (e.g. placing water), so a placed source begins flowing immediately.
// Cite: net.minecraft.world.level.block.LiquidBlock.neighborChanged / .onPlace -> scheduleTick.
func (t *TickLoop) scheduleFluidNeighborsOnEdit(pos pk.Position) {
	if cf := t.fluidAt(pos); cf.isFluid() {
		t.scheduleFluidTickKind(pos, cf) // a placed/edited fluid cell flows on its own delay
	}
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		if nf := t.fluidAt(np); nf.isFluid() {
			t.scheduleFluidTickKind(np, nf)
		}
	}
	if af := t.fluidAt(above(pos)); af.isFluid() {
		t.scheduleFluidTickKind(above(pos), af) // fluid above a freshly-broken block falls into it
	}
	if bf := t.fluidAt(below(pos)); bf.isFluid() {
		t.scheduleFluidTickKind(below(pos), bf)
	}
}

// opposite returns the reverse of a horizontal direction (Direction.getOpposite).
func opposite(d pk.Position) pk.Position {
	return pk.Position{X: -d.X, Y: -d.Y, Z: -d.Z}
}

// affectsFlow ports FlowingFluid.affectsFlow(FluidState): a neighbour contributes to the flow
// gradient iff it is empty OR the SAME fluid type as this one. An air neighbour (empty) counts
// as a "hole" the fluid slopes toward; a foreign fluid (water beside lava) does not.
//
//	javap net.minecraft.world.level.material.FlowingFluid.affectsFlow:
//	  return fluidState.isEmpty() || fluidState.getType().isSame(this);
func (f fluidState) affectsFlow(o fluidState) bool {
	return !o.isFluid() || o.sameKind(f)
}

// getFlow ports net.minecraft.world.level.material.FlowingFluid.getFlow(BlockGetter, BlockPos,
// FluidState) - the horizontal flow vector of a fluid cell, the gradient over its 4 horizontal
// neighbours (verified bytecode, 26.2 jar). It is the input to the entity current-push
// (EntityFluidInteraction.update -> Tracker.accumulateCurrent). Algorithm, 1:1:
//
//	dx = 0, dz = 0
//	for d in HORIZONTAL:                                  // horizontalDirs, same order
//	  np = pos + d
//	  nf = getFluidState(np)
//	  if !affectsFlow(nf): continue                       // foreign fluid -> skip
//	  ownHeight = nf.getOwnHeight()                       // amount / 9.0f
//	  heightDiff = 0
//	  if ownHeight == 0:                                  // neighbour is empty (air) here
//	     if !blocksMotion(np):                            // and not a wall
//	        belowF = getFluidState(np.below())
//	        if affectsFlow(belowF):
//	           bh = belowF.getOwnHeight()
//	           if bh > 0: heightDiff = this.getOwnHeight() - (bh - 0.8888889f)   // 8/9
//	  else if ownHeight > 0:
//	     heightDiff = this.getOwnHeight() - ownHeight
//	  if heightDiff != 0:
//	     dx += d.stepX * heightDiff;  dz += d.stepZ * heightDiff
//	v = Vec3(dx, 0, dz)
//	if this.FALLING:                                      // a falling column pushed against a wall
//	  for d in HORIZONTAL:
//	     np = pos + d
//	     if isSolidFace(np, d) || isSolidFace(np.above(), d):
//	        v = v.normalize().add(0, -6.0, 0); break
//	return v.normalize()
//
// FLOAT ops: getOwnHeight/heightDiff are float32 (amount/9.0f, the 0.8888889f == 8/9 literal, the
// stepX/Z*heightDiff products) accumulated into a float64 dx/dz - mirror the f2d widening exactly
// (each neighbour's float product is widened to double, then added). The FALLING -6.0 down-boost
// makes a waterfall shove outward. isSolidFace is the v1 blocksMotion subset (isSolidAt, the same
// canPassThroughWall simplification the rest of fluid.go uses - 17-RESEARCH Pattern 5). Cite:
// net.minecraft.world.level.material.FlowingFluid.getFlow / affectsFlow / FluidState.getOwnHeight.
func (t *TickLoop) getFlow(pos pk.Position, f fluidState) vec3d {
	var dx, dz float64
	ownHeight := f.ownHeight() // this.getOwnHeight() - hoisted (constant across the loop)
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		nf := t.fluidAt(np)
		if !f.affectsFlow(nf) {
			continue
		}
		nHeight := nf.ownHeight()
		var heightDiff float32
		if nHeight == 0 {
			// The neighbour holds no fluid at this cell. If it is not a wall, look one cell DOWN:
			// a lower fluid there means this cell slopes toward the drop (the getFlow "step down"
			// case), offset by 8/9 (the falling-column height bias).
			if !t.isSolidAt(np) {
				belowF := t.fluidAt(below(np))
				if f.affectsFlow(belowF) {
					bh := belowF.ownHeight()
					if bh > 0 {
						heightDiff = ownHeight - (bh - 0.8888889)
					}
				}
			}
		} else if nHeight > 0 {
			heightDiff = ownHeight - nHeight
		}
		if heightDiff != 0 {
			dx += float64(float32(d.X) * heightDiff)
			dz += float64(float32(d.Z) * heightDiff)
		}
	}
	v := vec3d{dx, 0, dz}
	// FALLING boost: a falling fluid against a solid face (at this level or one above) is shoved
	// outward and hard down (-6.0) so a waterfall pushes an entity away from the wall it pours down.
	if f.falling {
		for _, d := range horizontalDirs {
			np := plus(pos, d)
			if t.isSolidAt(np) || t.isSolidAt(above(np)) {
				v = v.normalize().add(0, -6.0, 0)
				break
			}
		}
	}
	return v.normalize()
}

// ownHeight ports FluidState.getOwnHeight (== FlowingFluid.getOwnHeight): amount / 9.0f (a float
// divide). A non-fluid cell has height 0. Cite net.minecraft.world.level.material.FlowingFluid.
// getOwnHeight (`getAmount(); i2f; ldc 9.0f; fdiv`).
func (f fluidState) ownHeight() float32 {
	if !f.isFluid() {
		return 0
	}
	return float32(f.amount) / 9.0
}
