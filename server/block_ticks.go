package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// block_ticks.go — SUB-BLOCKTICK: the server-side wiring of the general scheduled-block-tick
// subsystem (level/ticks.LevelTicks) into the tick loop, plus its first real consumer
// (SugarCaneBlock). This is the Go port of the net.minecraft.server.level.ServerLevel side of
// the vanilla scheduled-tick flow:
//
//	ServerLevel.scheduleTick(pos, block, delay[, priority])  -> blockTicks.schedule(createTick(...))
//	ServerLevel.tick: blockTicks.tick(gameTime, 65536, this::tickBlock)
//	ServerLevel.tickBlock(pos, block): if (getBlockState(pos).is(block)) state.tick(level, pos, random)
//
// CITE: ServerLevel.scheduleTick / ServerLevel.tick / ServerLevel.tickBlock; Level.createTick /
// Level.nextSubTickCount; LevelTicks.tick.
//
// ============================================================================================
// DECISION — the existing fluid loop (fluid_schedule.go / fluid.go) COEXISTS, it is NOT migrated.
// ============================================================================================
// Sulfur already ships a self-contained one-off scheduled-fluid loop (a per-gametime bucket map,
// fluidScheduleQueue) that drains in tickFluids. That loop is a faithful slice of FlowingFluid
// behavior and currently WORKS. Migrating fluids onto this general LevelTicks would mean porting
// the fluid `i` type identity, the Fluid.tick dispatch, and re-validating every water-spread
// test — out of scope for v1 and a needless regression risk. So in v1 the two coexist:
//   - blockTicks (this file) is the GENERAL LevelTicks<Block> for scheduled BLOCK ticks
//     (sugar cane now; redstone/crops/etc later), the vanilla ServerLevel.blockTicks.
//   - fluidSchedule (fluid_schedule.go) remains the bespoke fluid loop, the de-facto
//     ServerLevel.fluidTicks, untouched.
// The on-disk format unifies them anyway (both serialize via save.SavedTickNBT into the chunk's
// block_ticks / fluid_ticks lists), so a future plan can migrate fluids onto LevelTicks without
// changing the save format. Until then: no fluid behavior changes, no broken water.

// blockTickType is the payload type T of the block-tick LevelTicks — a block's identity, keyed
// by its resource-location string (the vanilla ScheduledTick<Block> type is the Block singleton;
// the on-disk codec stores it as the block id, so the id IS the stable identity and the natural
// comparable Go key). A scheduled block tick says "re-evaluate the block of THIS kind at THIS
// pos at THIS time" — exactly vanilla's (Block, BlockPos) pair. CITE: ScheduledTick<Block> /
// SavedTick.codec `i` field.
type blockTickType string

const (
	// sugarCaneTickType is the block id sugar-cane ticks are scheduled/dispatched under.
	sugarCaneTickType blockTickType = "minecraft:sugar_cane"
)

// maxAllowedBlockTicks is net.minecraft.server.level.ServerLevel's drain cap — the int 65536
// passed to blockTicks.tick(gameTime, 65536, this::tickBlock). It bounds how many scheduled
// ticks may FIRE in a single game-time; an overflowing backlog is rescheduled to the next
// game-time (LevelTicks.rescheduleLeftoverContainers) rather than firing unboundedly. CITE:
// ServerLevel.tick (`blockTicks.tick(gameTime, 65536, ...)`).
const maxAllowedBlockTicks = 65536

// ensureBlockTicks lazily constructs the level-wide block-tick manager. The per-chunk tick gate
// (vanilla ServerLevel::shouldTickBlocksAt) is "is this chunk loaded?" in v1 — a chunk with a
// container registered is by definition loaded/tickable, so the gate returns true (a future
// plan can tighten it to the real tick-distance check). Tick-owned.
func (t *TickLoop) ensureBlockTicks() *ticks.LevelTicks[blockTickType] {
	if t.blockTicks == nil {
		t.blockTicks = ticks.NewLevelTicks[blockTickType](func(chunkKey int64) bool {
			return true // v1: a chunk with a registered container is tickable (loaded)
		})
	}
	return t.blockTicks
}

// nextSubTick is net.minecraft.world.level.Level.nextSubTickCount(): post-increment the level
// sub-tick counter, returning the value BEFORE the increment (so the first draw is 0). It feeds
// each ScheduledTick's subTickOrder. CITE: Level.nextSubTickCount (`return subTickCount++`).
func (t *TickLoop) nextSubTick() int64 {
	v := t.blockTickSubCounter
	t.blockTickSubCounter++
	return v
}

// registerChunkBlockTicks adds a chunk's per-chunk tick container to the level manager when the
// chunk becomes ready, and UNPACKS any on-disk pending ticks against the current game-time
// (LevelChunkTicks.unpack), so a loaded chunk's saved ticks become live. If the chunk had no
// saved ticks it registers an empty container. CITE: ServerLevel chunk-load wiring
// (addContainer + unpack). Tick-owned; safe to call once per chunk becoming ready.
func (t *TickLoop) registerChunkBlockTicks(pos level.ChunkPos, container *ticks.LevelChunkTicks[blockTickType]) {
	mgr := t.ensureBlockTicks()
	mgr.AddContainer(pos[0], pos[1], container)
	container.Unpack(t.gametime)
}

// scheduleBlockTick is net.minecraft.world.level.ScheduledTickAccess.scheduleTick(pos, block,
// delay) (the 3-arg form, NORMAL priority): build a ScheduledTick at triggerTick = gameTime +
// delay with a fresh subTickOrder and enqueue it. A tick for an unloaded chunk is dropped by
// LevelTicks.schedule (no container). CITE: ScheduledTickAccess.scheduleTick(pos, block, delay)
// -> Level.createTick -> LevelTicks.schedule.
func (t *TickLoop) scheduleBlockTick(pos pk.Position, typ blockTickType, delay int) {
	t.scheduleBlockTickWithPriority(pos, typ, delay, ticks.PriorityNormal)
}

// scheduleBlockTickWithPriority is the 4-arg scheduleTick(pos, block, delay, priority). CITE:
// ScheduledTickAccess.scheduleTick(pos, block, delay, priority).
func (t *TickLoop) scheduleBlockTickWithPriority(pos pk.Position, typ blockTickType, delay int, priority ticks.TickPriority) {
	mgr := t.ensureBlockTicks()
	tick := ticks.NewScheduledTick(typ, pos, t.gametime+int64(delay), priority, t.nextSubTick())
	mgr.Schedule(tick)
}

// hasScheduledBlockTick reports whether (pos, typ) already has a pending scheduled tick — the
// vanilla LevelTickAccess.hasScheduledTick guard a block uses before re-scheduling (so it does
// not pile up duplicate ticks). CITE: LevelTicks.hasScheduledTick.
func (t *TickLoop) hasScheduledBlockTick(pos pk.Position, typ blockTickType) bool {
	if t.blockTicks == nil {
		return false
	}
	return t.blockTicks.HasScheduledTick(pos, typ)
}

// packChunkBlockTicks serializes a chunk's pending block ticks to the on-disk SavedTickNBT list
// (the chunk's `block_ticks` field), packing each live tick's absolute triggerTick back to a
// relative delay against the current game-time (LevelChunkTicks.pack -> SavedTick.toSavedTick).
// A chunk with no registered container or no ticks yields nil (the save shape omits the field).
// This is the SAVE half of the chunk tick round-trip; a chunk-flush caller folds the result into
// the chunk's block_ticks before serializing. CITE: LevelChunkTicks.pack.
func (t *TickLoop) packChunkBlockTicks(pos level.ChunkPos) []save.SavedTickNBT {
	if t.blockTicks == nil {
		return nil
	}
	container := t.blockTicks.Container(pos[0], pos[1])
	if container == nil {
		return nil
	}
	saved := container.Pack(t.gametime)
	if len(saved) == 0 {
		return nil
	}
	out := make([]save.SavedTickNBT, 0, len(saved))
	for _, s := range saved {
		out = append(out, save.SavedTickNBT{
			ID:       string(s.Type),
			X:        int32(s.Pos.X),
			Y:        int32(s.Pos.Y),
			Z:        int32(s.Pos.Z),
			Delay:    s.Delay,
			Priority: int32(s.Priority.Value()),
		})
	}
	return out
}

// loadChunkBlockTicks builds a per-chunk tick container seeded from the on-disk SavedTickNBT list
// (a freshly-LOADED chunk's `block_ticks`), then registers it with the level manager and unpacks
// it against the current game-time so the saved ticks become live. An empty/nil list registers an
// empty container (so the chunk can still receive live schedules). This is the LOAD half of the
// chunk tick round-trip. CITE: LevelChunkTicks(List) + addContainer + unpack.
func (t *TickLoop) loadChunkBlockTicks(pos level.ChunkPos, savedTicks []save.SavedTickNBT) {
	saved := make([]ticks.SavedTick[blockTickType], 0, len(savedTicks))
	for _, s := range savedTicks {
		saved = append(saved, ticks.SavedTick[blockTickType]{
			Type:     blockTickType(s.ID),
			Pos:      pk.Position{X: int(s.X), Y: int(s.Y), Z: int(s.Z)},
			Delay:    s.Delay,
			Priority: ticks.PriorityByValue(int(s.Priority)),
		})
	}
	container := ticks.NewLevelChunkTicksFromSaved(saved)
	t.registerChunkBlockTicks(pos, container)
}

// tickScheduledBlocks is the SUB-BLOCKTICK drain phase — the Go port of the ServerLevel.tick
// "tickPending"/"blockTicks" section: blockTicks.tick(gameTime, 65536, this::tickBlock). It
// drains every block tick due at the current game-time (bounded at 65536) in the vanilla
// deterministic order and dispatches each to tickBlock. A nil manager (no chunk ever registered
// a container) is a cheap no-op. Tick-owned; called from tickWorld (no new tick phase — keeps
// TestTickPhaseOrder green), BEFORE the fluid pass so block ticks precede fluid ticks exactly as
// ServerLevel.tick drains blockTicks before fluidTicks. CITE: ServerLevel.tick block/fluid drain
// order; LevelTicks.tick.
func (t *TickLoop) tickScheduledBlocks() {
	if t.blockTicks == nil {
		return
	}
	t.blockTicks.Tick(t.gametime, maxAllowedBlockTicks, t.tickBlock)
}

// tickBlock is net.minecraft.server.level.ServerLevel.tickBlock(pos, block): re-read the block
// state at pos and, ONLY if it is still the scheduled block kind (`state.is(block)`), invoke the
// block's tick handler. A block that changed/was removed since the tick was scheduled fires
// nothing (the stale-tick guard). CITE: ServerLevel.tickBlock (`BlockState s = getBlockState(
// pos); if (s.is(block)) s.tick(this, pos, this.random)`).
func (t *TickLoop) tickBlock(pos pk.Position, typ blockTickType) {
	if t.world == nil {
		return
	}
	state, ok := t.world.GetBlock(pos, dimMinY)
	if !ok {
		return // unloaded/out-of-range: nothing to tick (getBlockState would be air, not `block`)
	}
	switch typ {
	case sugarCaneTickType:
		if !block.IsSugarCane(state) {
			return // state.is(SUGAR_CANE) false: stale tick, fire nothing
		}
		t.sugarCaneTick(state, pos)
	default:
		// Unknown scheduled type (a future block whose handler is not yet ported): no-op. The
		// tick was still dequeued, matching vanilla's `is(block)` guard failing for a stale type.
	}
}
