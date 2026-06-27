package ticks

// tickHeap is a minimal binary min-heap of ScheduledTick[T] ordered by a less function.
// It is the Go stand-in for vanilla's java.util.PriorityQueue<ScheduledTick<T>> (used by
// LevelChunkTicks.tickQueue with DRAIN_ORDER, and by LevelTicks.containersToTick with
// CONTAINER_DRAIN_ORDER). A hand-rolled heap keeps the type concrete (no container/heap
// boxing into interface{}), and its peek/poll/add semantics match PriorityQueue exactly:
// peek/poll return the LEAST element per `less`, ties broken by the comparator (NOT by
// insertion order — PriorityQueue is not stable, but the DRAIN_ORDER comparator is a total
// order over distinct (triggerTick, priority, subTickOrder) triples, so the drain order is
// deterministic regardless).
//
// All access is single-threaded (the owning LevelTicks is tick-owned — TICK-05), so the
// heap needs no synchronization.
type tickHeap[T comparable] struct {
	data []ScheduledTick[T]
	less func(a, b ScheduledTick[T]) bool
}

func newTickHeap[T comparable](less func(a, b ScheduledTick[T]) bool) *tickHeap[T] {
	return &tickHeap[T]{less: less}
}

func (h *tickHeap[T]) size() int { return len(h.data) }

// peek returns the least element and true, or the zero value and false when empty —
// mirroring PriorityQueue.peek (returns null when empty).
func (h *tickHeap[T]) peek() (ScheduledTick[T], bool) {
	if len(h.data) == 0 {
		var zero ScheduledTick[T]
		return zero, false
	}
	return h.data[0], true
}

// add inserts v and sifts it up — PriorityQueue.add/offer.
func (h *tickHeap[T]) add(v ScheduledTick[T]) {
	h.data = append(h.data, v)
	h.siftUp(len(h.data) - 1)
}

// poll removes and returns the least element and true, or false when empty —
// PriorityQueue.poll.
func (h *tickHeap[T]) poll() (ScheduledTick[T], bool) {
	n := len(h.data)
	if n == 0 {
		var zero ScheduledTick[T]
		return zero, false
	}
	top := h.data[0]
	last := h.data[n-1]
	h.data = h.data[:n-1]
	if n-1 > 0 {
		h.data[0] = last
		h.siftDown(0)
	}
	return top, true
}

func (h *tickHeap[T]) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(h.data[i], h.data[parent]) {
			break
		}
		h.data[i], h.data[parent] = h.data[parent], h.data[i]
		i = parent
	}
}

func (h *tickHeap[T]) siftDown(i int) {
	n := len(h.data)
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && h.less(h.data[right], h.data[left]) {
			smallest = right
		}
		if !h.less(h.data[smallest], h.data[i]) {
			break
		}
		h.data[i], h.data[smallest] = h.data[smallest], h.data[i]
		i = smallest
	}
}

// snapshot returns a copy of every queued tick (heap order, NOT sorted) — the backing for
// LevelChunkTicks.getAll / pack (which sorts its own copy). It never mutates the heap.
func (h *tickHeap[T]) snapshot() []ScheduledTick[T] {
	out := make([]ScheduledTick[T], len(h.data))
	copy(out, h.data)
	return out
}
