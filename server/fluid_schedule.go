package server

import (
	"sort"

	pk "github.com/imhinotori/sulfur/net/packet"
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
