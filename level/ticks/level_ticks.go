package ticks

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chunkKey packs a block position's CHUNK coordinates into a single int64 — the Go analogue
// of net.minecraft.world.level.ChunkPos.pack(BlockPos) == ChunkPos.asLong(x>>4, z>>4) ==
// ((long)chunkZ & 0xFFFFFFFFL) << 32 | ((long)chunkX & 0xFFFFFFFFL). The arithmetic shift
// (>>4 on a signed int) is the negative-correct block->chunk mapping (the same one the world
// manager uses). The exact bit layout only has to be a stable, collision-free key per chunk;
// this mirrors vanilla so the keying is identical. CITE: ChunkPos.pack / ChunkPos.asLong.
func chunkKey(pos pk.Position) int64 {
	cx := int64(int32(pos.X >> 4))
	cz := int64(int32(pos.Z >> 4))
	return (cz&0xFFFFFFFF)<<32 | (cx & 0xFFFFFFFF)
}

// chunkKeyXZ packs already-chunk-space coordinates (the addContainer/removeContainer entry
// points take a ChunkPos, not a BlockPos). CITE: ChunkPos.pack() == asLong(x, z).
func chunkKeyXZ(chunkX, chunkZ int32) int64 {
	return (int64(chunkZ)&0xFFFFFFFF)<<32 | (int64(chunkX) & 0xFFFFFFFF)
}

// noTickValue is the Long2LongMap.defaultReturnValue for nextTickForContainer — Long.MAX_VALUE,
// meaning "this container has no due tick". CITE: LevelTicks.lambda$new$0 (defaultReturnValue(
// Long.MAX_VALUE)).
const noTickValue = int64(0x7FFFFFFFFFFFFFFF)

// LevelTicks is net.minecraft.world.ticks.LevelTicks<T> — the LEVEL-WIDE scheduled-tick
// manager. It owns every loaded chunk's LevelChunkTicks container (allContainers), tracks each
// container's next due game-time (nextTickForContainer), and on each tick(gameTime, maxTicks,
// run) drains up to maxTicks due ticks ACROSS all chunks in the vanilla deterministic order
// (earliest triggerTick, then priority, then subTickOrder) and feeds each to the run callback.
//
// SINGLE-OWNER (TICK-05): every field is touched only on the level's tick goroutine — the
// drain runs synchronously inside the tick phase. No xsync/locks (vanilla uses plain
// fastutil maps + a PriorityQueue, all single-threaded under the server tick).
//
// CITE: net.minecraft.world.ticks.LevelTicks.
type LevelTicks[T comparable] struct {
	// tickCheck is the LongPredicate(chunkKey) gate: "should this chunk tick this game-time?"
	// (vanilla wires ServerLevel::shouldTickBlocksAt). A chunk that fails the check is NOT
	// drained (its due tick is left for a later game-time when the chunk is tickable again).
	tickCheck func(chunkKey int64) bool

	allContainers       map[int64]*LevelChunkTicks[T] // Long2ObjectMap<LevelChunkTicks<T>>
	nextTickForContainer map[int64]int64              // Long2LongMap (default Long.MAX_VALUE)

	// containersToTick is the per-drain working set of chunks with a due tick this game-time,
	// ordered by their HEAD tick's INTRA_TICK_DRAIN_ORDER (CONTAINER_DRAIN_ORDER). It is a
	// PriorityQueue<LevelChunkTicks<T>>; rebuilt each tick by collectTicks and cleared by
	// cleanupAfterTick.
	containersToTick *containerHeap[T]

	// toRunThisTick is the ArrayDeque<ScheduledTick<T>> of ticks selected to fire this game-time
	// (FIFO — they were already selected in drain order, so a plain queue preserves it). Capped
	// at maxTicks by canScheduleMoreTicks.
	toRunThisTick []ScheduledTick[T]

	// alreadyRunThisTick is the List<ScheduledTick<T>> of ticks already fired this game-time
	// (vanilla keeps it for clearArea/copyArea bookkeeping; retained for fidelity, cleared each
	// tick).
	alreadyRunThisTick []ScheduledTick[T]

	// toRunThisTickSet is the ObjectOpenCustomHashSet(UNIQUE_TICK_HASH) of (type,pos) currently
	// selected to run — guards willTickThisTick. Mutated alongside toRunThisTick.
	toRunThisTickSet map[tickKey[T]]struct{}
}

// NewLevelTicks builds a level-wide manager with the given per-chunk tick gate. CITE:
// LevelTicks(LongPredicate tickCheck).
func NewLevelTicks[T comparable](tickCheck func(chunkKey int64) bool) *LevelTicks[T] {
	return &LevelTicks[T]{
		tickCheck:            tickCheck,
		allContainers:        make(map[int64]*LevelChunkTicks[T]),
		nextTickForContainer: make(map[int64]int64),
		containersToTick:     newContainerHeap[T](),
		toRunThisTickSet:     make(map[tickKey[T]]struct{}),
	}
}

// nextTick reads nextTickForContainer with the Long.MAX_VALUE default (an absent key means
// "no due tick"). CITE: nextTickForContainer.defaultReturnValue(Long.MAX_VALUE).
func (l *LevelTicks[T]) nextTick(key int64) int64 {
	if v, ok := l.nextTickForContainer[key]; ok {
		return v
	}
	return noTickValue
}

// AddContainer registers a loaded chunk's container (LevelTicks.addContainer). It records the
// container, seeds nextTickForContainer from the container's head (if any), and installs the
// updateContainerScheduling callback so a live schedule into this chunk updates its due time.
// CITE: LevelTicks.addContainer.
func (l *LevelTicks[T]) AddContainer(chunkX, chunkZ int32, container *LevelChunkTicks[T]) {
	key := chunkKeyXZ(chunkX, chunkZ)
	l.allContainers[key] = container
	if head, ok := container.Peek(); ok {
		l.nextTickForContainer[key] = head.TriggerTick
	}
	container.setOnTickAdded(l.chunkScheduleUpdater)
}

// RemoveContainer unregisters a chunk's container on unload (LevelTicks.removeContainer): drop
// it from allContainers + nextTickForContainer and detach the callback. CITE:
// LevelTicks.removeContainer.
func (l *LevelTicks[T]) RemoveContainer(chunkX, chunkZ int32) {
	key := chunkKeyXZ(chunkX, chunkZ)
	c := l.allContainers[key]
	delete(l.allContainers, key)
	delete(l.nextTickForContainer, key)
	if c != nil {
		c.setOnTickAdded(nil)
	}
}

// Container returns the registered container for the chunk covering pos, or nil — a helper
// for callers that schedule via the container directly (the unpack-on-load path).
func (l *LevelTicks[T]) Container(chunkX, chunkZ int32) *LevelChunkTicks[T] {
	return l.allContainers[chunkKeyXZ(chunkX, chunkZ)]
}

// Schedule enqueues a live tick (LevelTicks.schedule). It routes the tick to the container of
// its chunk; a tick for an unloaded chunk is DROPPED (vanilla logs and returns — we drop
// silently, the same observable outcome: no tick is queued). CITE: LevelTicks.schedule
// (`LevelChunkTicks c = allContainers.get(ChunkPos.pack(t.pos())); if (c == null) {
// Util.logAndPauseIfInIde(...); return;} c.schedule(t)`).
func (l *LevelTicks[T]) Schedule(t ScheduledTick[T]) {
	c := l.allContainers[chunkKey(t.Pos)]
	if c == nil {
		return // tick for an unloaded chunk: dropped (vanilla logs-and-returns)
	}
	c.Schedule(t)
}

// chunkScheduleUpdater is LevelTicks.lambda$new$1 — the onTickAdded callback installed on each
// container. When a new tick is added to a container AND it becomes the container's new head
// (earliest), refresh nextTickForContainer so the next collectTicks sees the sooner due time.
// CITE: LevelTicks.lambda$new$1 (`if (tick.equals(container.peek())) updateContainerScheduling(tick)`).
func (l *LevelTicks[T]) chunkScheduleUpdater(container *LevelChunkTicks[T], tick ScheduledTick[T]) {
	if head, ok := container.Peek(); ok && head == tick {
		l.updateContainerScheduling(tick)
	}
}

// updateContainerScheduling sets nextTickForContainer[chunk(tick.pos)] = tick.triggerTick.
// CITE: LevelTicks.updateContainerScheduling.
func (l *LevelTicks[T]) updateContainerScheduling(tick ScheduledTick[T]) {
	l.nextTickForContainer[chunkKey(tick.Pos)] = tick.TriggerTick
}

// Tick is net.minecraft.world.ticks.LevelTicks.tick(long gameTime, int maxAllowedTicks,
// BiConsumer<BlockPos,T> ticker) — the per-game-time drain. It collects the due ticks across
// all tickable chunks (bounded by maxAllowedTicks), runs each via ticker, then cleans up. The
// three phases mirror vanilla's profiler sections "collect" / "run" / "cleanup". CITE:
// LevelTicks.tick.
func (l *LevelTicks[T]) Tick(gameTime int64, maxAllowedTicks int, ticker func(pos pk.Position, typ T)) {
	l.collectTicks(gameTime, maxAllowedTicks)
	l.runCollectedTicks(ticker)
	l.cleanupAfterTick()
}

// collectTicks: sort the tickable containers by their due head, drain up to maxAllowedTicks due
// ticks into toRunThisTick, then reschedule the leftover containers. CITE: LevelTicks.collectTicks.
func (l *LevelTicks[T]) collectTicks(gameTime int64, maxAllowedTicks int) {
	l.sortContainersToTick(gameTime)
	l.drainContainers(gameTime, maxAllowedTicks)
	l.rescheduleLeftoverContainers()
}

// sortContainersToTick scans nextTickForContainer for every chunk whose due time <= gameTime
// AND that passes the tickCheck, pushing each onto containersToTick (ordered by its head tick's
// INTRA_TICK_DRAIN_ORDER). It prunes stale entries: a missing container, or an empty container,
// is removed; a container whose head moved PAST gameTime has its nextTick refreshed to the head's
// triggerTick (so a future tick is not lost). CITE: LevelTicks.sortContainersToTick.
func (l *LevelTicks[T]) sortContainersToTick(gameTime int64) {
	for key, due := range l.nextTickForContainer {
		if due > gameTime {
			continue // not due yet (the > gameTime branch's `continue`)
		}
		container := l.allContainers[key]
		if container == nil {
			delete(l.nextTickForContainer, key) // stale entry: container unloaded
			continue
		}
		head, ok := container.Peek()
		if !ok {
			delete(l.nextTickForContainer, key) // empty container: nothing to tick
			continue
		}
		if head.TriggerTick > gameTime {
			// Head moved to a later game-time (e.g. its earlier tick was already drained):
			// refresh the due time, do not tick this game-time. CITE: entry.setValue(head.triggerTick).
			l.nextTickForContainer[key] = head.TriggerTick
			continue
		}
		if l.tickCheck(key) {
			delete(l.nextTickForContainer, key) // claimed for this drain; re-added by rescheduleLeftover if it has more
			l.containersToTick.add(container)
		}
		// else: chunk not tickable this game-time — leave its nextTick entry untouched so it is
		// retried next game-time (vanilla: the `if (tickCheck.test(key))` guard simply skips).
	}
}

// drainContainers polls due containers head-first and pulls every due tick from each (subject to
// the maxAllowedTicks cap), interleaving by INTRA_TICK_DRAIN_ORDER against the next container's
// head so the GLOBAL drain order is exactly DRAIN_ORDER across chunks. CITE: LevelTicks.drainContainers.
func (l *LevelTicks[T]) drainContainers(gameTime int64, maxAllowedTicks int) {
	for l.canScheduleMoreTicks(maxAllowedTicks) {
		container, ok := l.containersToTick.poll()
		if !ok {
			return // no more due containers
		}
		// Pull the head of this container unconditionally (it was the queue's least), then keep
		// pulling from it while its next head still beats the NEXT container's head.
		t, _ := container.Poll()
		l.scheduleForThisTick(t)
		l.drainFromCurrentContainer(container, gameTime, maxAllowedTicks)

		// If the container still has a due tick and we can take more, re-queue it; else update its
		// scheduling to its (now later / nil) head. CITE: drainContainers tail.
		if head, ok := container.Peek(); ok {
			if head.TriggerTick <= gameTime && l.canScheduleMoreTicks(maxAllowedTicks) {
				l.containersToTick.add(container)
			} else {
				l.updateContainerScheduling(head)
			}
		}
	}
}

// drainFromCurrentContainer keeps pulling due ticks from `current` as long as (a) we can take
// more, (b) current's head is still due (<= gameTime), and (c) current's head still sorts at or
// before the NEXT container's head under INTRA_TICK_DRAIN_ORDER (so a tick in another chunk that
// should fire first is not skipped). CITE: LevelTicks.drainFromCurrentContainer.
func (l *LevelTicks[T]) drainFromCurrentContainer(current *LevelChunkTicks[T], gameTime int64, maxAllowedTicks int) {
	if !l.canScheduleMoreTicks(maxAllowedTicks) {
		return
	}
	// nextHead is the head of the NEXT-most-urgent container (the queue head after `current` was
	// polled out), or none. CITE: `LevelChunkTicks next = queue.peek(); ScheduledTick nextHead =
	// next != null ? next.peek() : null`.
	var nextHead ScheduledTick[T]
	haveNext := false
	if peekContainer, ok := l.containersToTick.peek(); ok {
		if h, ok2 := peekContainer.Peek(); ok2 {
			nextHead, haveNext = h, true
		}
	}
	for l.canScheduleMoreTicks(maxAllowedTicks) {
		head, ok := current.Peek()
		if !ok {
			return
		}
		if head.TriggerTick > gameTime {
			return // current's next tick is for a later game-time
		}
		if haveNext && intraTickDrainCompare(head, nextHead) > 0 {
			return // the next container's head should fire before current's — yield to it
		}
		t, _ := current.Poll()
		l.scheduleForThisTick(t)
	}
}

// rescheduleLeftoverContainers refreshes nextTickForContainer for any container still in the
// working queue when the drain stopped early (maxAllowedTicks hit), so its remaining due ticks
// are retried next game-time. CITE: LevelTicks.rescheduleLeftoverContainers.
func (l *LevelTicks[T]) rescheduleLeftoverContainers() {
	for _, container := range l.containersToTick.snapshot() {
		head, ok := container.Peek()
		if !ok {
			continue // empty — nothing to reschedule (vanilla passes null to updateContainerScheduling,
			// which would NPE on pos(); an empty leftover container cannot occur in vanilla because a
			// container only enters the queue with a due head and is re-added only with a due head — so
			// skipping the empty case is the faithful, panic-free equivalent).
		}
		l.updateContainerScheduling(head)
	}
}

// scheduleForThisTick appends t to the run-this-tick queue and marks its (type,pos) selected.
// CITE: LevelTicks.scheduleForThisTick (toRunThisTick.add(t)) — the set add is folded here from
// the vanilla flow where toRunThisTickSet is populated alongside.
func (l *LevelTicks[T]) scheduleForThisTick(t ScheduledTick[T]) {
	l.toRunThisTick = append(l.toRunThisTick, t)
	l.toRunThisTickSet[t.key()] = struct{}{}
}

// canScheduleMoreTicks reports whether the run-this-tick queue is still below the cap. CITE:
// LevelTicks.canScheduleMoreTicks (toRunThisTick.size() < maxAllowedTicks).
func (l *LevelTicks[T]) canScheduleMoreTicks(maxAllowedTicks int) bool {
	return len(l.toRunThisTick) < maxAllowedTicks
}

// runCollectedTicks fires every selected tick in order, clearing its set entry and recording it
// in alreadyRunThisTick, then invoking ticker(pos, type). CITE: LevelTicks.runCollectedTicks.
func (l *LevelTicks[T]) runCollectedTicks(ticker func(pos pk.Position, typ T)) {
	for len(l.toRunThisTick) > 0 {
		t := l.toRunThisTick[0]
		l.toRunThisTick = l.toRunThisTick[1:]
		if len(l.toRunThisTickSet) > 0 {
			delete(l.toRunThisTickSet, t.key())
		}
		l.alreadyRunThisTick = append(l.alreadyRunThisTick, t)
		ticker(t.Pos, t.Type)
	}
}

// cleanupAfterTick resets the per-tick working state. CITE: LevelTicks.cleanupAfterTick (clear
// toRunThisTick, containersToTick, alreadyRunThisTick, toRunThisTickSet).
func (l *LevelTicks[T]) cleanupAfterTick() {
	l.toRunThisTick = l.toRunThisTick[:0]
	l.containersToTick.clear()
	l.alreadyRunThisTick = l.alreadyRunThisTick[:0]
	clear(l.toRunThisTickSet)
}

// HasScheduledTick reports whether a tick for (pos, typ) is queued in pos's chunk container.
// CITE: LevelTicks.hasScheduledTick.
func (l *LevelTicks[T]) HasScheduledTick(pos pk.Position, typ T) bool {
	c := l.allContainers[chunkKey(pos)]
	if c == nil {
		return false
	}
	return c.HasScheduledTick(pos, typ)
}

// WillTickThisTick reports whether (pos, typ) is selected to fire in the in-progress drain.
// CITE: LevelTicks.willTickThisTick (toRunThisTickSet.contains(probe)).
func (l *LevelTicks[T]) WillTickThisTick(pos pk.Position, typ T) bool {
	_, ok := l.toRunThisTickSet[ProbeScheduledTick(typ, pos).key()]
	return ok
}

// Count is LevelTicks.count: the total live queued ticks across every container. CITE:
// LevelTicks.count (sum of allContainers' counts).
func (l *LevelTicks[T]) Count() int {
	n := 0
	for _, c := range l.allContainers {
		n += c.Count()
	}
	return n
}
