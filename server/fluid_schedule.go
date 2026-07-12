package server

import (
	"sort"

	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

// fluid_schedule.go (GAMEPLAY-05) is the net-new scheduled-block-tick queue. The world's
// ChunkManager has no tick scheduler, so this is the Go analogue of vanilla
// ServerLevel.scheduleTick(pos, fluid, delay) (net.minecraft.server.level.ServerLevel):
// a fluid block schedules its next FlowingFluid.tick for gametime+getTickDelay, and the
// fluid pass (tickFluids in fluid.go) drains the bucket due at the current gametime.
//
// Ownership: a *fluidScheduleQueue is held by TickLoop.fluidSchedule (declared in tick.go by
// 17-01); it is lazily constructed inside tickFluids (a nil-check), so this plan never edits
// SetWorld/tick.go. All access is on the tick goroutine (TICK-05).

// scheduledFluidTick is one pending fluid tick: the block position to re-evaluate.
type scheduledFluidTick struct {
	pos pk.Position
}

// fluidScheduleQueue is a per-gametime bucket map keyed by the absolute due gametime. A bucket
// holds every position scheduled to tick at that gametime; drainDue removes and returns the
// bucket in a DETERMINISTIC order (sorted by packed pos) so the simulation is reproducible
// run-to-run (17-RESEARCH Pitfall 2: nondeterministic map iteration => water shape changes on
// reload). 17-01 stubbed `type fluidScheduleQueue struct{}` in fluid.go; that stub is removed
// in this plan's fluid.go overwrite so this is the sole definition.
type fluidScheduleQueue struct {
	buckets map[int64][]scheduledFluidTick
}

// newFluidScheduleQueue returns an empty queue ready to accept schedule() calls.
func newFluidScheduleQueue() *fluidScheduleQueue {
	return &fluidScheduleQueue{buckets: make(map[int64][]scheduledFluidTick)}
}

// schedule enqueues a fluid tick for pos at the absolute dueGametime. Duplicate (pos,
// dueGametime) entries are allowed — fluidTick is idempotent (recomputes getNewLiquid), and a
// duplicate only costs one extra recompute, so dedup is not required for correctness. Mirrors
// ServerLevel.scheduleTick.
func (q *fluidScheduleQueue) schedule(pos pk.Position, dueGametime int64) {
	q.buckets[dueGametime] = append(q.buckets[dueGametime], scheduledFluidTick{pos: pos})
}

// drainDue removes the bucket scheduled for gametime, sorts it by packed pos for deterministic
// processing (Pitfall 2), and returns it. A gametime with no bucket returns nil. The bucket is
// deleted so it never drains twice.
func (q *fluidScheduleQueue) drainDue(gametime int64) []scheduledFluidTick {
	bucket, ok := q.buckets[gametime]
	if !ok {
		return nil
	}
	delete(q.buckets, gametime)
	sort.Slice(bucket, func(i, j int) bool {
		return packPos(bucket[i].pos) < packPos(bucket[j].pos)
	})
	return bucket
}

// empty reports whether no ticks are scheduled (every bucket drained). Used by tests to drain
// the queue to a fixed point and prove termination.
func (q *fluidScheduleQueue) empty() bool {
	return len(q.buckets) == 0
}

// pending counts every scheduled tick still queued across all future buckets. Used only by the
// fluid cost instrumentation (tickFluids) to report the backlog depth — it is O(buckets), called
// at most once per gametick and only when the firehose is on.
func (q *fluidScheduleQueue) pending() int {
	n := 0
	for _, b := range q.buckets {
		n += len(b)
	}
	return n
}

// packPos packs a block position into a single int64 for a deterministic, allocation-free sort
// key (and a stable ordering across runs). The 26/12/26-bit field split mirrors the vanilla
// BlockPos packing (net.minecraft.core.BlockPos.asLong) so the ordering is well-defined across
// the full overworld y-range; the exact bit layout is irrelevant to correctness here — only
// that it is a total, deterministic order over distinct positions.
func packPos(p pk.Position) int64 {
	const (
		xzBits = 26
		yBits  = 12
		xzMask = (int64(1) << xzBits) - 1
		yMask  = (int64(1) << yBits) - 1
	)
	return ((int64(p.X) & xzMask) << (yBits + xzBits)) |
		((int64(p.Z) & xzMask) << yBits) |
		(int64(p.Y) & yMask)
}

// packChunkFluidTicks serializes the pending fluid ticks that fall inside the chunk at cp into the
// on-disk SavedTickNBT list (the chunk fluid_ticks field), the fluid twin of packChunkBlockTicks.
// Each pending (pos, dueGametime) in the flat schedule queue whose chunk matches cp becomes one
// SavedTick: its type id i is the fluid at the cell right now (minecraft:water / minecraft:lava, or
// the flowing form when not a source), its delay is dueGametime - gametime (relative, exactly
// SavedTick.toSavedTick), and its priority is NORMAL(0) -- fluids always schedule via the 3-arg
// scheduleTick(pos, fluid, delay). A cell that is no longer a fluid is skipped (a stale schedule for
// a drained cell writes nothing, matching how the reloaded tick would no-op). A nil queue / no
// matching ticks yields nil so the save shape omits the field. CITE: SerializableChunkData
// fluid_ticks (FLUID_TICKS_CODEC over List<SavedTick<Fluid>>); FlowingFluid.tick ->
// ServerLevel.scheduleTick(pos, fluid, delay).
func (t *TickLoop) packChunkFluidTicks(cp level.ChunkPos, gametime int64) []save.SavedTickNBT {
	if t.cur().fluidSchedule == nil {
		return nil
	}
	var out []save.SavedTickNBT
	for due, bucket := range t.cur().fluidSchedule.buckets {
		for _, st := range bucket {
			if int32(st.pos.X>>4) != cp[0] || int32(st.pos.Z>>4) != cp[1] {
				continue // a tick in another chunk: not this column business
			}
			id := t.fluidSavedTickID(st.pos)
			if id == "" {
				continue // no fluid at the cell now -> nothing to persist (drained/replaced)
			}
			out = append(out, save.SavedTickNBT{
				ID:       id,
				X:        int32(st.pos.X),
				Y:        int32(st.pos.Y),
				Z:        int32(st.pos.Z),
				Delay:    int32(due - gametime),
				Priority: 0, // fluid scheduleTick is the 3-arg NORMAL form (TickPriority.NORMAL == 0)
			})
		}
	}
	// Deterministic order (packed pos, then delay): map iteration is nondeterministic, so sort to a
	// stable on-disk shape run-to-run (the same Pitfall-2 discipline drainDue uses).
	sort.Slice(out, func(i, j int) bool {
		pi := packPos(pk.Position{X: int(out[i].X), Y: int(out[i].Y), Z: int(out[i].Z)})
		pj := packPos(pk.Position{X: int(out[j].X), Y: int(out[j].Y), Z: int(out[j].Z)})
		if pi != pj {
			return pi < pj
		}
		return out[i].Delay < out[j].Delay
	})
	return out
}

// fluidSavedTickID resolves the fluid registry id i a scheduled fluid tick at pos serializes under:
// minecraft:water for a water source, minecraft:lava for a lava source, and the flowing_* form when
// the cell is a non-source (flowing) fluid -- mirroring FluidState.getType() registry key. Empty
// when the cell holds no fluid. CITE: WaterFluid.Source/Flowing + LavaFluid registry ids.
func (t *TickLoop) fluidSavedTickID(pos pk.Position) string {
	f := t.fluidAt(pos)
	switch {
	case f.isWater:
		if f.source {
			return "minecraft:water"
		}
		return "minecraft:flowing_water"
	case f.isLava:
		if f.source {
			return "minecraft:lava"
		}
		return "minecraft:flowing_lava"
	default:
		return ""
	}
}

// loadChunkFluidTicks re-schedules the on-disk fluid ticks for a freshly loaded chunk (the fluid
// twin of loadChunkBlockTicks). Each SavedTick becomes a live schedule at gametime + delay; the
// fluid identity i is IGNORED because fluidTick recomputes the cell fluid from the world when it
// fires, so only (pos, delay) matter to resume the mid-spread flow. An empty list is a no-op. A
// negative delay (a tick that was due while the chunk was unloaded) clamps to 0 so it fires next
// tick, matching how LevelChunkTicks.unpack floors the trigger at the current game-time. Tick-owned.
// CITE: LevelChunkTicks.unpack (triggerTick = max(delay + gameTime, gameTime)); FlowingFluid.tick.
func (t *TickLoop) loadChunkFluidTicks(savedTicks []save.SavedTickNBT, gametime int64) {
	if len(savedTicks) == 0 {
		return
	}
	if t.cur().fluidSchedule == nil {
		t.cur().fluidSchedule = newFluidScheduleQueue()
	}
	for _, st := range savedTicks {
		due := gametime + int64(st.Delay)
		if due < gametime {
			due = gametime // clamp a past-due tick to fire next tick (unpack max(_, gameTime))
		}
		t.cur().fluidSchedule.schedule(pk.Position{X: int(st.X), Y: int(st.Y), Z: int(st.Z)}, due)
	}
}
