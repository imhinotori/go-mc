package starlark

import (
	"sync"
	"testing"

	"go.starlark.net/starlark"
)

// TestFrozenCrossGoroutine proves the cross-goroutine race-safety (T-21-07): a
// global frozen at load is READ from N reader goroutines and CALLED from M
// caller goroutines, each caller on its OWN fresh Thread (LoadedPlugin.Call
// makes a thread internally — Threads are NEVER shared, Pitfall 4). Frozen
// values are immutable, so the concurrent reads need no lock. This is the test
// that MUST run under the Docker -race (CGO=1) gate; it also passes CGO=0 as
// pure logic. t.Errorf (not t.Fatal) is used inside the spawned goroutines
// because t.Fatal from a non-test goroutine is illegal.
func TestFrozenCrossGoroutine(t *testing.T) {
	p, err := Load("testdata/greet.star")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	// N=8 reader goroutines: read the FROZEN `result` global concurrently (no
	// lock — frozen values are immutable).
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok := p.Global("result")
			if !ok {
				t.Errorf("frozen read: `result` global missing")
				return
			}
			if v.(starlark.String) != "hi" {
				t.Errorf("frozen read mismatch: got %v, want %q", v, "hi")
			}
		}()
	}

	// M=4 caller goroutines: each invokes greet on its OWN fresh Thread (Call
	// makes a fresh thread internally). The frozen fn value crosses; the Thread
	// does not.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := p.Call("greet", starlark.String("bob"))
			if err != nil {
				t.Errorf("call greet: %v", err)
				return
			}
			if out.(starlark.String) != "bob" {
				t.Errorf("greet return mismatch: got %v, want %q", out, "bob")
			}
		}()
	}

	wg.Wait()
}
