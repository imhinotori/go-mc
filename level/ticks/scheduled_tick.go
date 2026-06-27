package ticks

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// ScheduledTick is net.minecraft.world.ticks.ScheduledTick<T> — a LIVE pending tick:
//
//	record ScheduledTick<T>(T type, BlockPos pos, long triggerTick, TickPriority priority, long subTickOrder)
//
// triggerTick is the ABSOLUTE game-time the tick fires at (gameTime + delay, computed at
// schedule time by Level.createTick); subTickOrder is a monotonically-increasing tiebreak
// (Level.nextSubTickCount) so two ticks at the same triggerTick + priority fire in schedule
// order. The pos is made immutable in the vanilla ctor (BlockPos.immutable); pk.Position is
// already a value type, so Go gets that for free.
//
// CITE: net.minecraft.world.ticks.ScheduledTick (record + ctor).
type ScheduledTick[T comparable] struct {
	Type         T
	Pos          pk.Position
	TriggerTick  int64
	Priority     TickPriority
	SubTickOrder int64
}

// NewScheduledTick builds a ScheduledTick with an explicit priority — the 5-arg vanilla
// ctor ScheduledTick(T, BlockPos, long, TickPriority, long).
func NewScheduledTick[T comparable](typ T, pos pk.Position, triggerTick int64, priority TickPriority, subTickOrder int64) ScheduledTick[T] {
	return ScheduledTick[T]{Type: typ, Pos: pos, TriggerTick: triggerTick, Priority: priority, SubTickOrder: subTickOrder}
}

// ProbeScheduledTick is net.minecraft.world.ticks.ScheduledTick.probe(type, pos): a
// zero-trigger, NORMAL-priority, zero-subTickOrder tick used ONLY as a uniqueness key
// (its hash/equality is (type,pos) via UNIQUE_TICK_HASH — see tickKey). CITE:
// ScheduledTick.probe (new ScheduledTick(type, pos, 0L, NORMAL, 0L)).
func ProbeScheduledTick[T comparable](typ T, pos pk.Position) ScheduledTick[T] {
	return ScheduledTick[T]{Type: typ, Pos: pos, TriggerTick: 0, Priority: PriorityNormal, SubTickOrder: 0}
}

// ToSavedTick is net.minecraft.world.ticks.ScheduledTick.toSavedTick(long gameTime): packs
// the live tick to its on-disk form, converting the ABSOLUTE triggerTick back to a RELATIVE
// delay = (int)(triggerTick - gameTime). The subTickOrder is dropped (the on-disk order is
// re-derived from list position on load — see SavedTick.unpack). CITE:
// ScheduledTick.toSavedTick (new SavedTick(type, pos, (int)(triggerTick - gameTime), priority)).
func (s ScheduledTick[T]) ToSavedTick(gameTime int64) SavedTick[T] {
	return SavedTick[T]{Type: s.Type, Pos: s.Pos, Delay: int32(s.TriggerTick - gameTime), Priority: s.Priority}
}

// tickKey is the (type, pos) identity used by vanilla's UNIQUE_TICK_HASH strategy — the
// Hash.Strategy that makes the per-position ObjectOpenCustomHashSet treat two ticks as
// equal iff they share a block AND a type. It is the Go map key behind LevelChunkTicks'
// ticksPerPosition / LevelTicks' toRunThisTickSet (Go maps need a comparable key; vanilla
// uses a custom hash strategy to the same effect). CITE: ScheduledTick.UNIQUE_TICK_HASH
// (ScheduledTick$1 hashing/equating on pos + type).
type tickKey[T comparable] struct {
	typ T
	pos pk.Position
}

func (s ScheduledTick[T]) key() tickKey[T] { return tickKey[T]{typ: s.Type, pos: s.Pos} }

// drainOrderLess is net.minecraft.world.ticks.ScheduledTick.DRAIN_ORDER (lambda$static$0):
//
//	int c = Long.compare(a.triggerTick, b.triggerTick); if (c != 0) return c;
//	c = a.priority.compareTo(b.priority);                if (c != 0) return c;   // ORDINAL compare
//	return Long.compare(a.subTickOrder, b.subTickOrder);
//
// This is the per-chunk tickQueue ordering (the PriorityQueue comparator): earliest
// triggerTick first, then highest priority (lowest ordinal), then earliest subTickOrder.
// CITE: ScheduledTick.DRAIN_ORDER.
func drainOrderLess[T comparable](a, b ScheduledTick[T]) bool {
	if a.TriggerTick != b.TriggerTick {
		return a.TriggerTick < b.TriggerTick
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority // TickPriority.compareTo == ordinal compare; ordinal == our int value
	}
	return a.SubTickOrder < b.SubTickOrder
}

// intraTickDrainCompare is ScheduledTick.INTRA_TICK_DRAIN_ORDER (lambda$static$1) returned
// as a 3-way comparison (-1/0/+1) because LevelTicks.drainFromCurrentContainer compares the
// SIGN of the result against a peeked container head (not just a less-than). It orders by
// priority (ordinal) then subTickOrder — triggerTick is NOT consulted (within a single
// target tick every candidate already shares the triggerTick):
//
//	int c = a.priority.compareTo(b.priority); if (c != 0) return c;
//	return Long.compare(a.subTickOrder, b.subTickOrder);
//
// CITE: ScheduledTick.INTRA_TICK_DRAIN_ORDER.
func intraTickDrainCompare[T comparable](a, b ScheduledTick[T]) int {
	if a.Priority != b.Priority {
		if a.Priority < b.Priority {
			return -1
		}
		return 1
	}
	if a.SubTickOrder != b.SubTickOrder {
		if a.SubTickOrder < b.SubTickOrder {
			return -1
		}
		return 1
	}
	return 0
}

// SavedTick is net.minecraft.world.ticks.SavedTick<T> — the ON-DISK tick record:
//
//	record SavedTick<T>(T type, BlockPos pos, int delay, TickPriority priority)
//
// delay is RELATIVE to the chunk's save-time game-time (triggerTick - gameTime at save);
// on load it is turned back into an absolute triggerTick = gameTime + delay (unpack). The
// on-disk codec field names are: i (type), x, y, z (pos), t (delay), p (priority.value).
// CITE: SavedTick (record) + SavedTick.codec field names {i,x,y,z,t,p}.
type SavedTick[T comparable] struct {
	Type     T
	Pos      pk.Position
	Delay    int32
	Priority TickPriority
}

// Unpack is net.minecraft.world.ticks.SavedTick.unpack(long gameTime, long subTickOrder):
// turns the on-disk relative delay into an absolute triggerTick = gameTime + delay and
// re-attaches a fresh subTickOrder (the caller — LevelChunkTicks.unpack — derives it from
// list position so the load order is stable). CITE: SavedTick.unpack (new ScheduledTick(
// type, pos, gameTime + delay, priority, subTickOrder)).
func (s SavedTick[T]) Unpack(gameTime, subTickOrder int64) ScheduledTick[T] {
	return ScheduledTick[T]{
		Type:         s.Type,
		Pos:          s.Pos,
		TriggerTick:  gameTime + int64(s.Delay),
		Priority:     s.Priority,
		SubTickOrder: subTickOrder,
	}
}
