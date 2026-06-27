package ticks

// containerHeap is a binary min-heap of *LevelChunkTicks[T] ordered by CONTAINER_DRAIN_ORDER —
// net.minecraft.world.ticks.LevelTicks.CONTAINER_DRAIN_ORDER (lambda$static$0):
//
//	(a, b) -> ScheduledTick.INTRA_TICK_DRAIN_ORDER.compare(a.peek(), b.peek())
//
// i.e. two containers are ordered by their HEAD tick's (priority, subTickOrder). It is the Go
// stand-in for vanilla's PriorityQueue<LevelChunkTicks<T>>(CONTAINER_DRAIN_ORDER) used as
// LevelTicks.containersToTick. A container with no head (empty) never enters the queue in the
// vanilla flow, but the comparator guards the empty case defensively (an empty head sorts last)
// so a stray empty container can never panic the heap.
//
// Single-threaded (tick-owned) — no synchronization. CITE: LevelTicks.CONTAINER_DRAIN_ORDER.
type containerHeap[T comparable] struct {
	data []*LevelChunkTicks[T]
}

func newContainerHeap[T comparable]() *containerHeap[T] { return &containerHeap[T]{} }

// less orders by the two containers' head ticks under INTRA_TICK_DRAIN_ORDER. A container with
// no head sorts AFTER one with a head (defensive; vanilla never enqueues an empty container).
func (h *containerHeap[T]) less(a, b *LevelChunkTicks[T]) bool {
	ha, oka := a.Peek()
	hb, okb := b.Peek()
	switch {
	case !oka && !okb:
		return false
	case !oka:
		return false // a empty -> a sorts after b
	case !okb:
		return true // b empty -> a sorts before b
	}
	return intraTickDrainCompare(ha, hb) < 0
}

func (h *containerHeap[T]) add(v *LevelChunkTicks[T]) {
	h.data = append(h.data, v)
	i := len(h.data) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(h.data[i], h.data[parent]) {
			break
		}
		h.data[i], h.data[parent] = h.data[parent], h.data[i]
		i = parent
	}
}

func (h *containerHeap[T]) peek() (*LevelChunkTicks[T], bool) {
	if len(h.data) == 0 {
		return nil, false
	}
	return h.data[0], true
}

func (h *containerHeap[T]) poll() (*LevelChunkTicks[T], bool) {
	n := len(h.data)
	if n == 0 {
		return nil, false
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

func (h *containerHeap[T]) siftDown(i int) {
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

func (h *containerHeap[T]) snapshot() []*LevelChunkTicks[T] {
	out := make([]*LevelChunkTicks[T], len(h.data))
	copy(out, h.data)
	return out
}

func (h *containerHeap[T]) clear() { h.data = h.data[:0] }
