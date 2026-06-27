package ticks

import (
	"sort"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// LevelChunkTicks is net.minecraft.world.ticks.LevelChunkTicks<T> — the PER-CHUNK tick
// container. It owns a DRAIN_ORDER-ordered priority queue (tickQueue) of live ScheduledTicks
// for one chunk, a per-position uniqueness set (ticksPerPosition, the UNIQUE_TICK_HASH set),
// and — until unpacked — a pendingTicks list of on-disk SavedTicks loaded from region NBT.
//
// Lifecycle: a freshly-LOADED chunk's container holds its SavedTicks in pendingTicks and
// seeds ticksPerPosition with their (type,pos) probes, but does NOT enqueue them into the
// live tickQueue until unpack(gameTime) is called (which turns each relative delay into an
// absolute triggerTick — the chunk's game-time anchor is only known once the level is
// ticking). A freshly-GENERATED chunk's container starts empty (pendingTicks nil).
//
// All access is on the owning level's tick goroutine (TICK-05), so no synchronization.
//
// CITE: net.minecraft.world.ticks.LevelChunkTicks.
type LevelChunkTicks[T comparable] struct {
	tickQueue       *tickHeap[T]            // PriorityQueue<ScheduledTick<T>>(DRAIN_ORDER)
	pendingTicks    []SavedTick[T]          // loaded-but-not-yet-unpacked saved ticks (nil once unpacked)
	ticksPerPosition map[tickKey[T]]struct{} // ObjectOpenCustomHashSet(UNIQUE_TICK_HASH): (type,pos) dedup
	onTickAdded     func(*LevelChunkTicks[T], ScheduledTick[T]) // LevelTicks' chunkScheduleUpdater hook
}

// NewLevelChunkTicks builds an empty container (the no-arg vanilla ctor) — for a
// freshly-generated chunk that has no saved ticks. CITE: LevelChunkTicks().
func NewLevelChunkTicks[T comparable]() *LevelChunkTicks[T] {
	return &LevelChunkTicks[T]{
		tickQueue:        newTickHeap[T](drainOrderLess[T]),
		ticksPerPosition: make(map[tickKey[T]]struct{}),
	}
}

// NewLevelChunkTicksFromSaved builds a container seeded with on-disk SavedTicks (the
// list-arg vanilla ctor LevelChunkTicks(List<SavedTick<T>>)). The saved ticks are held in
// pendingTicks (NOT enqueued yet) and their (type,pos) probes seed ticksPerPosition so a
// re-schedule of an already-pending position dedups even before unpack. CITE:
// LevelChunkTicks(List) — `this.pendingTicks = list; for each: ticksPerPosition.add(
// ScheduledTick.probe(type, pos))`.
func NewLevelChunkTicksFromSaved[T comparable](saved []SavedTick[T]) *LevelChunkTicks[T] {
	c := NewLevelChunkTicks[T]()
	c.pendingTicks = saved
	for _, s := range saved {
		c.ticksPerPosition[ProbeScheduledTick(s.Type, s.Pos).key()] = struct{}{}
	}
	return c
}

// setOnTickAdded installs the LevelTicks callback fired whenever a NEW (deduped) tick is
// enqueued, so the level can re-evaluate this chunk's next-tick scheduling. CITE:
// LevelChunkTicks.setOnTickAdded.
func (c *LevelChunkTicks[T]) setOnTickAdded(f func(*LevelChunkTicks[T], ScheduledTick[T])) {
	c.onTickAdded = f
}

// Peek returns the earliest-due tick (heap head) without removing it. CITE:
// LevelChunkTicks.peek (tickQueue.peek()).
func (c *LevelChunkTicks[T]) Peek() (ScheduledTick[T], bool) { return c.tickQueue.peek() }

// Poll removes and returns the earliest-due tick, clearing its (type,pos) from the
// uniqueness set so the position can be re-scheduled. CITE: LevelChunkTicks.poll
// (`ScheduledTick<T> t = tickQueue.poll(); if (t != null) ticksPerPosition.remove(t); return t`).
func (c *LevelChunkTicks[T]) Poll() (ScheduledTick[T], bool) {
	t, ok := c.tickQueue.poll()
	if ok {
		delete(c.ticksPerPosition, t.key())
	}
	return t, ok
}

// Schedule enqueues t IFF its (type,pos) is not already queued (the UNIQUE_TICK_HASH
// dedup): vanilla `if (ticksPerPosition.add(t)) scheduleUnchecked(t)`. A duplicate
// (same type+pos already pending) is dropped — vanilla never double-schedules a position.
// CITE: LevelChunkTicks.schedule.
func (c *LevelChunkTicks[T]) Schedule(t ScheduledTick[T]) {
	k := t.key()
	if _, exists := c.ticksPerPosition[k]; exists {
		return // ticksPerPosition.add returned false: already scheduled at this (type,pos)
	}
	c.ticksPerPosition[k] = struct{}{}
	c.scheduleUnchecked(t)
}

// scheduleUnchecked enqueues t into the heap and fires onTickAdded (if installed) — the
// dedup-bypassing path used by Schedule and by unpack (whose ticks are already unique).
// CITE: LevelChunkTicks.scheduleUnchecked (`tickQueue.add(t); if (onTickAdded != null)
// onTickAdded.accept(this, t)`).
func (c *LevelChunkTicks[T]) scheduleUnchecked(t ScheduledTick[T]) {
	c.tickQueue.add(t)
	if c.onTickAdded != nil {
		c.onTickAdded(c, t)
	}
}

// HasScheduledTick reports whether a tick for (typ, pos) is queued — a probe lookup in the
// uniqueness set. CITE: LevelChunkTicks.hasScheduledTick (ticksPerPosition.contains(probe)).
func (c *LevelChunkTicks[T]) HasScheduledTick(pos pk.Position, typ T) bool {
	_, ok := c.ticksPerPosition[ProbeScheduledTick(typ, pos).key()]
	return ok
}

// Count is LevelChunkTicks.count: the number of live queued ticks (tickQueue.size). It does
// NOT count pendingTicks (those are not yet live). CITE: LevelChunkTicks.count.
func (c *LevelChunkTicks[T]) Count() int { return c.tickQueue.size() }

// Pack is net.minecraft.world.ticks.LevelChunkTicks.pack(long gameTime): produce the on-disk
// SavedTick list. It (1) starts with any still-pending (never-unpacked) saved ticks verbatim,
// then (2) takes a COPY of the live tickQueue, SORTS it by SUB_TICK_ORDERING (subTickOrder
// only — Comparator.comparingLong(ScheduledTick::subTickOrder)), and appends each as a
// SavedTick (delay = triggerTick - gameTime). The sub-tick sort makes the saved order match
// the schedule order so a reload re-derives stable subTickOrders from list position. CITE:
// LevelChunkTicks.pack.
func (c *LevelChunkTicks[T]) Pack(gameTime int64) []SavedTick[T] {
	out := make([]SavedTick[T], 0, c.tickQueue.size())
	if c.pendingTicks != nil {
		out = append(out, c.pendingTicks...)
	}
	live := c.tickQueue.snapshot()
	sort.SliceStable(live, func(i, j int) bool {
		return live[i].SubTickOrder < live[j].SubTickOrder // SUB_TICK_ORDERING
	})
	for _, t := range live {
		out = append(out, t.ToSavedTick(gameTime))
	}
	return out
}

// Unpack is net.minecraft.world.ticks.LevelChunkTicks.unpack(long gameTime): promote the
// pendingTicks (loaded from disk) into the live tickQueue, anchoring each to the current
// game-time. The subTickOrder is derived from list position as a NEGATIVE run starting at
// -len(pending) and incrementing (so loaded ticks sort BEFORE any tick scheduled live this
// session, whose subTickOrder comes from the positive level counter). After promotion
// pendingTicks is cleared (nil) so a second Unpack is a no-op. CITE: LevelChunkTicks.unpack
// (`int order = -pendingTicks.size(); for (SavedTick t : pendingTicks) scheduleUnchecked(
// t.unpack(gameTime, order++)); pendingTicks = null`).
func (c *LevelChunkTicks[T]) Unpack(gameTime int64) {
	if c.pendingTicks == nil {
		return
	}
	order := int64(-len(c.pendingTicks))
	for _, s := range c.pendingTicks {
		c.scheduleUnchecked(s.Unpack(gameTime, order))
		order++
	}
	c.pendingTicks = nil
}
