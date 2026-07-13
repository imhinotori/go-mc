package server

import (
	"sync"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

func relightTestPos(cx, cz int32) pk.Position {
	return pk.Position{X: int(cx * 16), Y: 64, Z: int(cz * 16)}
}

func TestRelightQueueZeroValueAndCoalescing(t *testing.T) {
	q := new(relightQueue)
	q.add(dimOverworld, level.ChunkPos{3, 5}, relightTestPos(3, 5))
	q.add(dimOverworld, level.ChunkPos{3, 5}, relightTestPos(3, 5))
	if got := len(q.drain()[dimOverworld]); got != 1 {
		t.Fatalf("drained columns = %d, want 1", got)
	}
	if got := len(q.drain()); got != 0 {
		t.Fatalf("second drain dimensions = %d, want 0", got)
	}
}

func TestRelightQueueConcurrentFanIn(t *testing.T) {
	q := newRelightQueue()
	const writers, perWriter = 16, 64
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				cx := int32(writer*perWriter + i)
				q.add(dimOverworld, level.ChunkPos{cx, 0}, relightTestPos(cx, 0))
			}
		}(writer)
	}
	wg.Wait()
	if got := len(q.drain()[dimOverworld]); got != writers*perWriter {
		t.Fatalf("drained columns = %d, want %d", got, writers*perWriter)
	}
}

func TestRelightQueueMergeCarryKeepsOldAge(t *testing.T) {
	q := newRelightQueue()
	old := level.ChunkPos{1, 1}
	q.add(dimOverworld, old, relightTestPos(1, 1))
	carry, carrySequence := q.drainBatch()

	// Simulate an edit arriving while RelightColumns runs. Merging carry must
	// coalesce it and preserve the older scheduling sequence.
	q.add(dimOverworld, old, relightTestPos(1, 1))
	q.add(dimOverworld, level.ChunkPos{2, 2}, relightTestPos(2, 2))
	q.mergeCarryBatch(carry, carrySequence)
	snap, sequence := q.drainBatch()
	if got := len(snap[dimOverworld]); got != 2 {
		t.Fatalf("merged columns = %d, want 2", got)
	}
	if got, want := sequence[dimOverworld][old], carrySequence[dimOverworld][old]; got != want {
		t.Fatalf("merged sequence = %d, want old sequence %d", got, want)
	}
}

func TestPlanRelightForDimBudgetAndOrder(t *testing.T) {
	snap := map[level.ChunkPos]pk.Position{
		{0, 0}: relightTestPos(0, 0),
		{5, 5}: relightTestPos(5, 5),
	}
	sequence := map[level.ChunkPos]uint64{{0, 0}: 0, {5, 5}: 1}
	affected, carry, carrySequence := planRelightForDimFIFO(snap, sequence, 9)
	if len(affected) != 9 {
		t.Fatalf("affected columns = %d, want one full 3x3", len(affected))
	}
	if !chunksSorted(affected) {
		t.Fatalf("affected columns are not sorted: %v", affected)
	}
	if _, ok := carry[level.ChunkPos{5, 5}]; !ok || carrySequence[level.ChunkPos{5, 5}] != 1 {
		t.Fatalf("carry = %v sequence=%v, want whole second edit", carry, carrySequence)
	}

	for i := 0; i < 16; i++ {
		nextAffected, nextCarry, _ := planRelightForDimFIFO(snap, sequence, 9)
		if !equalChunks(affected, nextAffected) || len(nextCarry) != len(carry) {
			t.Fatalf("plan changed on run %d: affected=%v carry=%v", i, nextAffected, nextCarry)
		}
	}
}

func TestRelightQueueCarryCannotStarve(t *testing.T) {
	q := newRelightQueue()
	for _, col := range []level.ChunkPos{{0, 0}, {100, 0}, {200, 0}} {
		q.add(dimOverworld, col, relightTestPos(col[0], col[1]))
	}

	snap, sequence := q.drainBatch()
	_, carry, carrySequence := planRelightForDimFIFO(snap[dimOverworld], sequence[dimOverworld], 9)
	for tick, want := range []level.ChunkPos{{100, 0}, {200, 0}} {
		newer := level.ChunkPos{int32(-100 * (tick + 1)), 0}
		q.add(dimOverworld, newer, relightTestPos(newer[0], newer[1]))
		q.mergeCarryBatch(
			map[int]map[level.ChunkPos]pk.Position{dimOverworld: carry},
			map[int]map[level.ChunkPos]uint64{dimOverworld: carrySequence},
		)
		snap, sequence = q.drainBatch()
		var affected []level.ChunkPos
		affected, carry, carrySequence = planRelightForDimFIFO(snap[dimOverworld], sequence[dimOverworld], 9)
		if !containsChunk(affected, want) {
			t.Fatalf("tick %d affected %v, want oldest deferred %v", tick+2, affected, want)
		}
	}
}

func TestRelightChangedConcurrentUsesBlockCoordinates(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	oldState := block.ToStateID[block.Air{}]
	newState := block.ToStateID[block.Stone{}]
	const writers, perWriter = 8, 100
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				cx := int32(writer*perWriter + i)
				loop.relightChanged(dimOverworld, mgr, relightTestPos(cx, 0), oldState, newState)
			}
		}(writer)
	}
	wg.Wait()
	if got := len(loop.dirtyRelight.drain()[dimOverworld]); got != writers*perWriter {
		t.Fatalf("drained columns = %d, want %d", got, writers*perWriter)
	}
}

func chunksSorted(chunks []level.ChunkPos) bool {
	for i := 1; i < len(chunks); i++ {
		if chunks[i-1][0] > chunks[i][0] || chunks[i-1][0] == chunks[i][0] && chunks[i-1][1] > chunks[i][1] {
			return false
		}
	}
	return true
}

func equalChunks(a, b []level.ChunkPos) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsChunk(chunks []level.ChunkPos, target level.ChunkPos) bool {
	for _, chunk := range chunks {
		if chunk == target {
			return true
		}
	}
	return false
}
