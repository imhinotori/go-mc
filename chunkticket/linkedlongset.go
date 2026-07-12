package chunkticket

// linkedLongSet is a minimal insertion-ordered long set, the behavioural equivalent of
// FastUtil LongLinkedOpenHashSet as used by LeveledPriorityQueue: add is idempotent and
// preserves first-insertion order, removeFirstLong pops the oldest still-present element,
// and remove deletes by value. Order matters: the priority queue relies on FIFO within a
// priority bucket so the propagation visits nodes in the same order as vanilla.
type linkedLongSet struct {
	order []int64        // insertion order, with tombstones (present map is the truth)
	head  int            // index of the next candidate for removeFirstLong
	set   map[int64]bool // membership
}

func newLinkedLongSet() *linkedLongSet {
	return &linkedLongSet{set: make(map[int64]bool)}
}

func (s *linkedLongSet) isEmpty() bool { return len(s.set) == 0 }

// add mirrors LongLinkedOpenHashSet.add(long): appends only on first insertion.
func (s *linkedLongSet) add(v int64) bool {
	if s.set[v] {
		return false
	}
	s.set[v] = true
	s.order = append(s.order, v)
	return true
}

// remove mirrors LongLinkedOpenHashSet.remove(long) by value (leaves a tombstone in the
// order slice that removeFirstLong/compaction skips).
func (s *linkedLongSet) remove(v int64) bool {
	if !s.set[v] {
		return false
	}
	delete(s.set, v)
	return true
}

// removeFirstLong mirrors LongLinkedOpenHashSet.removeFirstLong(): returns and removes
// the oldest still-present element. Skips tombstones left by remove.
func (s *linkedLongSet) removeFirstLong() int64 {
	for s.head < len(s.order) {
		v := s.order[s.head]
		s.head++
		if s.set[v] {
			delete(s.set, v)
			// Occasionally compact so a long-lived queue does not grow the order slice
			// unbounded; purely an allocation concern, no behavioural effect.
			if s.head > 1024 && s.head*2 > len(s.order) {
				s.compact()
			}
			return v
		}
	}
	return 0
}

func (s *linkedLongSet) compact() {
	rest := s.order[s.head:]
	kept := make([]int64, 0, len(s.set))
	for _, v := range rest {
		if s.set[v] {
			kept = append(kept, v)
		}
	}
	s.order = kept
	s.head = 0
}
