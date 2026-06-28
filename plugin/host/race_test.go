package host

import (
	"sync"
	"testing"
)

// TestEmitRace fires Emit over the frozen hooks from multiple goroutines
// concurrently. The hooks map is read-only after load and the callables are
// frozen, and each Emit builds a FRESH thread per call — so concurrent dispatch
// must be race-clean under -race (CGO=1 Docker gate). The counter is atomic so
// the asserting read does not itself introduce a race.
func TestEmitRace(t *testing.T) {
	m, c := loadGreeter(t)
	payload := BlockBreakEvent{X: 5, Y: 70, Z: 5, State: 3, PlayerID: 9}

	const goroutines = 8
	const perG = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				m.Emit(EventBlockBreak, payload)
			}
		}()
	}
	wg.Wait()

	if got := c.n.Load(); got != int64(goroutines*perG) {
		t.Fatalf("concurrent Emit: counter = %d, want %d", got, goroutines*perG)
	}
}
