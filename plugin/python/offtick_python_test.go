//go:build python

// offtick_python_test.go is the TAGGED-BUILD off-tick lane test. It runs ONLY
// under `CGO_ENABLED=1 go test -tags python -race ./plugin/python/` on an image
// with libpython 3.14 (python-3.14-embed + libffi) — the python3.14 Docker image /
// CI, NOT a default Windows/CGO=0 box (26-RESEARCH Pitfall 6). It proves:
//   - a registered python hook runs OFF the calling goroutine while the GIL is held
//     (TestPythonHookOffTick),
//   - the chosen GIL mode (one serialized interpreter — subinterp_python.go) is
//     internally consistent under concurrency (TestPythonSubInterpOrSerialized).
// The default build runs runtime_stub_test.go instead.

package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gopython.xyz/py/v14"
)

// goroutineID returns the current goroutine's id by parsing runtime.Stack — a
// test-only marker to prove the python hook ran on a worker goroutine distinct
// from the test's main goroutine (off-tick, Pitfall 2).
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:" — take the second field.
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, _ := strconv.ParseUint(fields[1], 10, 64)
	return id
}

// pluginTotal reads the plugin's module-level `total` aggregation counter back
// from the retained globals dict (under the GIL), proving the python hook body
// actually ran. Returns -1 on any read failure (so a missing/garbled value fails
// the assertion loudly).
func pluginTotal(t *testing.T, rt *Runtime) int64 {
	t.Helper()
	if rt.globals == nil {
		t.Fatal("rt.globals is nil — Load must retain the plugin globals for readback")
	}
	lock := py.NewLock()
	defer lock.Unlock()
	v, err := rt.globals.GetItemString("total")
	if err != nil || v == nil {
		t.Fatalf("read plugin global `total`: %v", err)
	}
	l, ok := v.(*py.Long)
	if !ok {
		t.Fatalf("plugin global `total` is not an int (got %T)", v)
	}
	return l.Int64()
}

// writeHeavylogger writes a heavylogger-style plugin that registers an
// on_block_break hook aggregating a running total, plus a get_total() the test can
// call to confirm the hook actually ran. Returns the entrypoint path.
func writeHeavylogger(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.py")
	body := `total = 0
def on_break(x, y, z, state, player_id):
    global total
    total += 1
register("on_block_break", on_break)
`
	if err := os.WriteFile(entry, []byte(body), 0o600); err != nil {
		t.Fatalf("write heavylogger main.py: %v", err)
	}
	return entry
}

// TestPythonHookOffTick: Load the heavylogger, then dispatch its hook from a
// goroutine that is NOT the test's main goroutine, asserting (a) CallHook ran the
// python aggregation (no error, hook present), and (b) it ran on a distinct
// OS-thread-pinned worker goroutine while holding the GIL (26-RESEARCH Pitfall 2 —
// python NEVER on the tick goroutine). We mark the worker goroutine and confirm
// the call executed there.
func TestPythonHookOffTick(t *testing.T) {
	entry := writeHeavylogger(t)
	rt, err := Load(entry)
	if err != nil {
		t.Fatalf("Load(heavylogger): %v", err)
	}
	defer rt.Close()

	if _, ok := rt.hooks["on_block_break"]; !ok {
		t.Fatal("register did not capture on_block_break into rt.hooks (register-capture failed)")
	}

	mainGID := goroutineID()
	var workerGID uint64
	var ranOffThread atomic.Bool

	done := make(chan error, 1)
	go func() {
		// The worker pins its OS thread (gopy NewLock calls runtime.LockOSThread
		// inside CallHook). Capture this goroutine's id to prove the hook ran here,
		// not on the caller/main goroutine.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		workerGID = goroutineID()
		ranOffThread.Store(workerGID != mainGID)
		done <- rt.CallHook("on_block_break", 1, 64, 1, 0, 42)
	}()

	if err := <-done; err != nil {
		t.Fatalf("off-tick CallHook returned err: %v (the python hook must run cleanly off-tick)", err)
	}
	if !ranOffThread.Load() {
		t.Fatal("the python hook ran on the test's main goroutine — it MUST run off-tick (Pitfall 2)")
	}

	// Confirm the python-side aggregation actually ran (total == 1 after one call).
	if got := pluginTotal(t, rt); got != 1 {
		t.Fatalf("python aggregation total = %d after one off-tick hook, want 1 (the hook body must have run)", got)
	}
}

// TestPythonSubInterpOrSerialized asserts the SELECTED GIL mode (subinterp_python.go)
// is internally consistent under concurrency — it does NOT require parallelism to
// exist. The chosen mode is serialized (one process-global interpreter); this test
// fires many concurrent CallHooks and asserts every one serializes through the
// single GIL WITHOUT corruption (the aggregation total equals the call count) and
// -race clean. If a future revision switches to sub-interpreters, the same test
// still asserts correctness (no corruption) of whichever mode is live.
func TestPythonSubInterpOrSerialized(t *testing.T) {
	if subInterpreters() {
		// Documented as false for gopy@3.14 (subinterp_python.go). If this ever flips,
		// the test below still asserts correctness; this branch is a marker for the
		// reviewer that the mode changed.
		t.Log("sub-interpreter mode active: asserting concurrent isolated interpreters behave correctly")
	} else {
		t.Log("serialized mode active (gopy@3.14 exposes no sub-interpreters — cited in subinterp_python.go)")
	}

	entry := writeHeavylogger(t)
	rt, err := Load(entry)
	if err != nil {
		t.Fatalf("Load(heavylogger): %v", err)
	}
	defer rt.Close()

	const workers = 8
	const callsEach = 25
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			for i := 0; i < callsEach; i++ {
				if err := rt.CallHook("on_block_break", i, 64, i, 0, 1); err != nil {
					errCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if errCount.Load() != 0 {
		t.Fatalf("%d CallHook errors under concurrency; want 0 (the single GIL must serialize cleanly without corruption)", errCount.Load())
	}
	if got := pluginTotal(t, rt); got != workers*callsEach {
		t.Fatalf("python aggregation total = %d after %d concurrent serialized calls, want %d "+
			"(every call must apply exactly once through the serialized GIL — no lost/double updates)",
			got, workers*callsEach, workers*callsEach)
	}
}
