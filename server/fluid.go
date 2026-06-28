package server

import (
	"github.com/imhinotori/sulfur/level"
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
	if t.only().fluidSchedule == nil {
		t.only().fluidSchedule = newFluidScheduleQueue()
	}
	if t.only().world == nil {
		return
	}
	due := t.only().fluidSchedule.drainDue(t.gametime)
	for _, st := range due {
		t.fluidTick(st.pos)
	}
	// Cost instrumentation for the "should the fluid sim move off-tick?" question: emit the
	// per-gametick fluid work (cells processed this tick + the queue depth still pending) ONLY
	// when there was work, so a busy session's fluid load is greppable (`grep 'ULTRA\[fluid\] cost'`)
	// without deciding the async optimization blind. Zero cost when the firehose is off.
	if len(due) > 0 {
		udebug("fluid", "cost gametime=%d processed=%d pendingAfter=%d", t.gametime, len(due), t.only().fluidSchedule.pending())
	}
}

// postProcessChunkFluids ports LevelChunk.postProcessGeneration's fluid step: when a freshly
// generated chunk goes live, run FluidState.tick EXACTLY ONCE on each cell the aquifer flagged
// for post-processing during fill (Chunk.PostProcessFluids = the markPosForPostProcessing list).
//
// This is the SAFE replacement for the cascading scan-and-schedule that was reverted twice. It
// does NOT scan all fluid cells, and it does NOT put cells on the recurring scheduleFluidTick
// queue at gen time — it kicks ONLY the small set of UNSTABLE BORDER cells the aquifer marked
// (shouldScheduleFluidUpdate), exactly once. That one fluidTick recomputes each border cell's
// liquid and spreads it (the normal getTickDelay-spaced queue takes over from there), which is
// what lets generated cave/aquifer water flow into a bordering air gap on load — without the
// runaway cascade (the aquifer only flags discontinuous borders, not entire flooded caves).
// Cite: net.minecraft.world.level.chunk.LevelChunk.postProcessGeneration ->
// FluidState.tick(level, pos, state) once per marked pos.
func (t *TickLoop) postProcessChunkFluids(pos level.ChunkPos, ch *level.Chunk) {
	if ch == nil || len(ch.PostProcessFluids) == 0 || t.only().world == nil {
		return
	}
	if t.only().fluidSchedule == nil {
		t.only().fluidSchedule = newFluidScheduleQueue()
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
		// exactly one FluidState.tick (just one pass later — invisible, fluid getTickDelay is 5).
		// spread() inside that tick re-schedules onward flow normally. CITE: LevelChunk.
		// postProcessGeneration runs FluidState.tick once per flagged cell (timing relaxed to the
		// next pass to honor the cross-chunk neighbor precondition vanilla gets from full-status).
		t.only().fluidSchedule.schedule(wp, t.gametime+1)
	}
}

// scheduleFluidTick schedules pos to re-evaluate getTickDelay(=5) ticks from now — the
// getTickDelay-spaced propagation (never a per-tick full-water scan). Lazily inits the queue.
func (t *TickLoop) scheduleFluidTick(pos pk.Position) {
	if t.only().fluidSchedule == nil {
		t.only().fluidSchedule = newFluidScheduleQueue()
	}
	t.only().fluidSchedule.schedule(pos, t.gametime+waterTickDelay)
}

// scheduleEmpty reports whether the fluid queue has no pending ticks (a fixed point). Used by
// tests to drain the simulation to completion and prove termination.
func (t *TickLoop) scheduleEmpty() bool {
	return t.only().fluidSchedule == nil || t.only().fluidSchedule.empty()
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
	id, ok := t.only().world.GetBlock(pos, dimMinY)
	if !ok {
		return fluidState{}
	}
	return decodeFluid(id)
}

// isSolidAt reports whether the block at pos is a solid (non-air, non-water) barrier. Fluid
// cannot pass through or replace a solid. This is the v1 passability subset of
// FlowingFluid.canPassThroughWall (the full VoxelShape face-occlusion is simplified to a
// solid/non-solid test for the water gate — 17-RESEARCH Pattern 5).
func (t *TickLoop) isSolidAt(pos pk.Position) bool {
	id, ok := t.only().world.GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded reads as non-solid (matches GetBlock's "no block here" contract)
	}
	if block.IsAir(id) {
		return false
	}
	if _, isWater := waterLevelOf(id); isWater {
		return false
	}
	return true
}

// canReplace reports whether the fluid can flow INTO pos: the cell is air or already this
// fluid (a solid blocks it). The v1 subset of FluidState.canBeReplacedWith + canHoldSpecificFluid.
func (t *TickLoop) canReplace(pos pk.Position) bool {
	return !t.isSolidAt(pos)
}

// fluidTick is the per-position FlowingFluid.tick body (net.minecraft.world.level.material.
// FlowingFluid.tick). For a non-source fluid: recompute getNewLiquid; if it differs from the
// current state, write it (AIR when empty) and re-schedule the affected cell. THEN always
// spread. A source never recomputes its own level (it stays full) but still spreads.
func (t *TickLoop) fluidTick(pos pk.Position) {
	cur := t.fluidAt(pos)
	if !cur.isWater {
		return // nothing to tick here (block changed since it was scheduled)
	}

	if !cur.source {
		next := t.getNewLiquid(pos)
		if !sameFluid(cur, next) {
			if !next.isWater {
				// drained to nothing -> air
				t.setFluidBlock(pos, airStateID())
				// neighbors may now need to re-flow into/around this newly-empty cell
				t.scheduleNeighbors(pos)
				return
			}
			t.setFluidBlock(pos, encodeFluid(next))
			t.scheduleFluidTick(pos)
			cur = next
		}
	}

	t.spread(pos, cur)
}

// setFluidBlock is the fluid sim's sole world-mutation primitive: it writes the new state AND
// broadcasts a ClientboundBlockUpdate to every player watching the column. Vanilla's
// Level.setBlock(pos, state, UPDATE_CLIENTS) does both — the fluid tick mutated the server world
// but the CLIENT never saw it, so water that flowed (filled a gap, drained, changed level) was
// invisible: the client kept rendering the chunk's load-time state. This made the fluid sim look
// inert from the client even though the server was flowing correctly (and made flowing/falling
// water never animate). Routing every fluid write through here keeps server and client in sync,
// exactly as a break/place edit does via reconcileEdit→broadcastBlockUpdate. Cite:
// net.minecraft.world.level.Level.setBlock with Block.UPDATE_CLIENTS. Returns whether the write
// changed the cell (mirrors world.SetBlock) so callers can gate on a real change.
func (t *TickLoop) setFluidBlock(pos pk.Position, state block.StateID) bool {
	if !t.only().world.SetBlock(pos, state, dimMinY) {
		return false // no change (already this state / unloaded): nothing to broadcast
	}
	t.broadcastBlockUpdate(pos, state)
	return true
}

// sameFluid reports whether two fluid states encode the same wire block (so no write/reschedule
// is needed). Air-vs-air and identical water states are "same".
func sameFluid(a, b fluidState) bool {
	if a.isWater != b.isWater {
		return false
	}
	if !a.isWater {
		return true
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
	maxAmount := 0
	sourceCount := 0
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		nf := t.fluidAt(np)
		if !nf.isWater {
			continue
		}
		// canPassThroughWall subset: the neighbor cell is fluid, so it can reach us unless a
		// solid wall sits between (no sub-block walls in v1 -> always passable here).
		if nf.source {
			sourceCount++
		}
		if nf.amount > maxAmount {
			maxAmount = nf.amount
		}
	}

	// Source conversion (FlowingFluid.getNewLiquid >= 2 sources branch).
	if sourceCount >= 2 && waterSourceConversion {
		belowPos := below(pos)
		belowSolid := t.isSolidAt(belowPos)
		belowFluid := t.fluidAt(belowPos)
		if belowSolid || (belowFluid.isWater && belowFluid.source) {
			return fluidState{isWater: true, source: true, amount: waterSourceAmount}
		}
	}

	// Falling rule: same fluid directly above + can pass down -> falling, full.
	abovePos := above(pos)
	af := t.fluidAt(abovePos)
	if af.isWater {
		return fluidState{isWater: true, falling: true, amount: waterSourceAmount}
	}

	newAmount := maxAmount - waterDropOff
	if newAmount <= 0 {
		return fluidState{} // empty
	}
	return fluidState{isWater: true, amount: newAmount}
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
	if !f.isWater {
		return
	}
	belowPos := below(pos)
	if t.canReplace(belowPos) {
		// Flow down: the cell below becomes falling, full.
		downState := fluidState{isWater: true, falling: true, amount: waterSourceAmount}
		t.spreadTo(belowPos, downState)
		if t.sourceNeighborCount(pos) >= 3 {
			t.spreadToSides(pos, f)
		}
		return
	}
	// Down is blocked. Spread sideways unless the below cell is a drainable hole that a
	// non-source flow should pour into instead of spreading.
	if f.source || !t.isHole(belowPos) {
		t.spreadToSides(pos, f)
	}
}

// spreadToSides flows to the horizontal neighbor(s) biased toward the nearest drop-off. PORT of
// FlowingFluid.spreadToSides + getSpread + getSlopeDistance (verified bytecode): the outgoing
// amount is amount-dropOff (or 7 when falling); getSpread keeps only the directions with the
// minimum slope distance to a hole within getSlopeFindDistance(=4).
func (t *TickLoop) spreadToSides(pos pk.Position, f fluidState) {
	outAmount := f.amount - waterDropOff
	if f.falling {
		outAmount = waterSourceAmount - waterDropOff // falling spreads at the full-minus-dropoff level (jar: 7)
	}
	if outAmount <= 0 {
		return
	}
	for _, d := range t.getSpread(pos) {
		np := plus(pos, d)
		t.spreadTo(np, fluidState{isWater: true, amount: outAmount})
	}
}

// getSpread returns the horizontal directions to spread into, biased toward the nearest
// drop-off via the slope-find. PORT of FlowingFluid.getSpread: for each passable horizontal
// neighbor that can hold fluid, compute its slope distance (0 if a hole sits below it); keep
// only the directions tied for the minimum distance.
func (t *TickLoop) getSpread(pos pk.Position) []pk.Position {
	minSlope := 1000
	var dirs []pk.Position
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		if !t.canReplace(np) {
			continue
		}
		var slope int
		if t.isHole(np) {
			slope = 0
		} else {
			slope = t.getSlopeDistance(np, 1, opposite(d))
		}
		if slope < minSlope {
			minSlope = slope
			dirs = dirs[:0]
		}
		if slope <= minSlope {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// getSlopeDistance is the recursive 8-direction slope-find within getSlopeFindDistance(=4). PORT
// of FlowingFluid.getSlopeDistance: from a cell at distance i, examine each horizontal neighbor
// (except the one we came from); if it can be passed through and is a hole (can flow down),
// return i; otherwise, if i < slopeFindDistance, recurse with i+1. Returns the minimum distance
// to a drop-off, or 1000 if none within range.
func (t *TickLoop) getSlopeDistance(pos pk.Position, dist int, excludeDir pk.Position) int {
	best := 1000
	for _, d := range horizontalDirs {
		if d == excludeDir {
			continue
		}
		np := plus(pos, d)
		if !t.canReplace(np) {
			continue
		}
		if t.isHole(np) {
			return dist
		}
		if dist < waterSlopeFindDistance {
			if r := t.getSlopeDistance(np, dist+1, opposite(d)); r < best {
				best = r
			}
		}
	}
	return best
}

// isHole reports whether fluid would drain downward THROUGH pos — pos itself must be passable (a
// solid cell is never a hole) AND the cell below pos can hold fluid. PORT of
// FlowingFluid.isWaterHole / SpreadContext.isHole (v1 subset). A solid floor cell is NOT a hole,
// so flow over a flat floor spreads sideways (it cannot fall through the floor).
func (t *TickLoop) isHole(pos pk.Position) bool {
	if !t.canReplace(pos) {
		return false // a solid cell cannot be passed through -> not a hole
	}
	return t.canReplace(below(pos))
}

// sourceNeighborCount counts the horizontal neighbors that are source blocks of this fluid.
// PORT of FlowingFluid.sourceNeighborCount.
func (t *TickLoop) sourceNeighborCount(pos pk.Position) int {
	n := 0
	for _, d := range horizontalDirs {
		nf := t.fluidAt(plus(pos, d))
		if nf.isWater && nf.source {
			n++
		}
	}
	return n
}

// spreadTo writes a fluid into a neighbor cell and schedules it to tick (so the flow continues
// on the getTickDelay-spaced queue). PORT of FlowingFluid.spreadTo (the setBlock + scheduleTick
// half — beforeDestroyingBlock/block-entity handling is out of v1 scope). Only writes when the
// target actually changes, so the queue reaches a fixed point (termination).
func (t *TickLoop) spreadTo(pos pk.Position, f fluidState) {
	if !t.canReplace(pos) {
		return
	}
	cur := t.fluidAt(pos)
	if !canBeReplacedWith(cur, f) {
		return // cannot overwrite a source or a stronger/equal flow (FluidState.canBeReplacedWith)
	}
	if sameFluid(cur, f) {
		return // no change: do not re-schedule (prevents infinite oscillation)
	}
	if t.setFluidBlock(pos, encodeFluid(f)) {
		udebug("fluid", "spreadTo (%d,%d,%d) amount=%d falling=%v source=%v", pos.X, pos.Y, pos.Z, f.amount, f.falling, f.source)
		t.scheduleFluidTick(pos)
	}
}

// canBeReplacedWith reports whether the fluid currently at a cell may be overwritten by the
// incoming fluid. PORT of FluidState.canBeReplacedWith semantics (v1 subset): air is always
// replaceable; an existing SOURCE is never replaced by a flow; an existing flow is replaced only
// by a STRONGER incoming flow (higher amount, or falling, which always dominates). This is what
// stops a level-1 spread from clobbering the level-0 source and prevents downgrade oscillation
// (termination — Pitfall 2).
func canBeReplacedWith(cur, incoming fluidState) bool {
	if !cur.isWater {
		return true // air -> always replaceable
	}
	if cur.source {
		return false // never overwrite a source
	}
	if incoming.falling {
		return true // a falling column dominates any flowing cell
	}
	if cur.falling {
		return false // a falling cell is not downgraded by a horizontal flow
	}
	return incoming.amount > cur.amount // only a stronger flow replaces a weaker one
}

// scheduleNeighbors schedules the 4 horizontal neighbors, the cell above, and the cell below to
// re-evaluate — used when this cell drains to air so adjacent fluid re-flows. Mirrors vanilla's
// neighborChanged-driven re-scheduling.
func (t *TickLoop) scheduleNeighbors(pos pk.Position) {
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		if t.fluidAt(np).isWater {
			t.scheduleFluidTick(np)
		}
	}
	if t.fluidAt(above(pos)).isWater {
		t.scheduleFluidTick(above(pos))
	}
}

// scheduleFluidNeighborsOnEdit is the port of Level.updateNeighborsAt → LiquidBlock.neighborChanged
// for a block EDIT (break or place). Vanilla: Level.setBlock notifies all 6 neighbors of the
// changed position; each neighbor that is a LiquidBlock runs neighborChanged, and if
// shouldSpreadLiquid it re-schedules its fluid tick (FlowingFluid.getTickDelay ticks out). Sulfur's
// break/place path (reconcileEdit) mutated the world but never ran this neighbor notification, so a
// block broken next to (or below) standing water left a permanent air gap — the adjacent water was
// never re-scheduled and so never flowed into the new hole. Schedule the fluid in EACH of the 6
// neighbors (the 4 horizontals + above + below); a scheduled water cell re-runs FlowingFluid.tick,
// which spreads down into / sideways toward the freshly-opened air. The edited cell itself is also
// scheduled if it is now water (e.g. placing water), so a placed source begins flowing immediately.
// Cite: net.minecraft.world.level.block.LiquidBlock.neighborChanged / .onPlace -> scheduleTick.
func (t *TickLoop) scheduleFluidNeighborsOnEdit(pos pk.Position) {
	if t.fluidAt(pos).isWater {
		t.scheduleFluidTick(pos) // a placed/edited water cell flows on its own delay
	}
	for _, d := range horizontalDirs {
		np := plus(pos, d)
		if t.fluidAt(np).isWater {
			t.scheduleFluidTick(np)
		}
	}
	if t.fluidAt(above(pos)).isWater {
		t.scheduleFluidTick(above(pos)) // water above a freshly-broken block falls into it
	}
	if t.fluidAt(below(pos)).isWater {
		t.scheduleFluidTick(below(pos))
	}
}

// opposite returns the reverse of a horizontal direction (Direction.getOpposite).
func opposite(d pk.Position) pk.Position {
	return pk.Position{X: -d.X, Y: -d.Y, Z: -d.Z}
}
