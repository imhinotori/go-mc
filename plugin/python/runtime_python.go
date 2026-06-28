//go:build python

// runtime_python.go is the OPT-IN (`-tags python`) face of the python package.
// It imports gopy (`gopython.xyz/py/v14`), which carries `import "C"` +
// `#cgo pkg-config: python-3.14-embed libffi`, so compiling THIS file forces cgo
// and links libpython 3.14. It NEVER compiles on the default build (the `python`
// tag excludes it), which is exactly what keeps `CGO_ENABLED=0 go build ./...`
// pure-Go static. The exported surface mirrors runtime_stub.go byte-for-byte
// (Available/Runtime/Load/Close/CallHook) so the package builds in both modes.
//
// gopy API citations (VERIFIED against the python3.14 branch per 26-RESEARCH.md
// "Standard Stack" + "Code Examples"):
//   - py.InitAndLock() *py.Lock     — Py_Initialize + GIL + LockOSThread, returns LOCKED  (lock.go)
//   - py.NewLock() *py.Lock         — GIL acquire + LockOSThread pin for THIS goroutine    (lock.go)
//   - (*py.Lock).Lock()/Unlock()    — re-acquire / release GIL + UnlockOSThread            (lock.go)
//   - py.RunFile(path, py.FileInput, globals, locals) (*py.Object, error)                  (run.go)
//   - (*py.Object).Base().CallGoArgs(args ...any) (*py.Object, error)                      (base.go)
//   - (*py.Object).Decref()         — drop a New Reference gopy returns                     (object)
//
// Off-tick discipline (PLUGIN-06): Load runs on the host goroutine before the
// tick loop; CallHook runs ONLY on an off-tick ants pool worker (never tickOnce),
// holding the GIL on the LockOSThread-pinned worker. Wave 2 wires the off-tick
// submit + the register-capture detail; this file only needs the load/init/run +
// the build to be REAL so `go build -tags python ./...` links libpython.

package python

import (
	"fmt"
	"sync"

	"gopython.xyz/py/v14"
)

// initOnce guards the process-global Py_Initialize: gopy embeds ONE CPython
// interpreter per process, so the first Load initializes + releases the
// bootstrap Lock; subsequent Loads re-acquire the GIL instead of re-initializing.
var initOnce sync.Once

// Available reports whether the CPython runtime is compiled in — true on the
// `-tags python` build.
func Available() bool { return true }

// Runtime owns the hooks a single plugin registered at load. The callables are
// *py.Object New References captured by the injected register(event, fn) builtin
// and dispatched OFF-TICK by CallHook.
type Runtime struct {
	hooks map[string]*py.Object // event name -> the registered python callable
}

// Load initializes CPython ONCE (process-global), runs the plugin's entrypoint
// .py (which calls register(event, fn) to publish its hooks), and returns the
// Runtime holding the captured callables. It runs on the host goroutine before
// the tick loop, holding the GIL only for the duration of the run.
//
// NOTE (Wave 2): the register-capture wiring (exposing a register builtin to the
// .py and harvesting its callables into rt.hooks) is fleshed out off-tick in
// Wave 2. The init + RunFile below are REAL so the tagged build links libpython.
func Load(entrypoint string) (*Runtime, error) {
	// Initialize the interpreter exactly once for the process. InitAndLock
	// returns a LOCKED bootstrap Lock; we release it after init so off-tick
	// workers can acquire the GIL via NewLock.
	initOnce.Do(func() {
		boot := py.InitAndLock() // Py_Initialize + GIL + LockOSThread (LOCKED)
		boot.Unlock()            // release the GIL after one-time setup
	})

	// Acquire the GIL for THIS goroutine to run the plugin body.
	lock := py.NewLock()
	defer lock.Unlock()

	rt := &Runtime{hooks: map[string]*py.Object{}}

	// Run the plugin entrypoint. Its register(event, fn) calls populate rt.hooks
	// (the register-builtin capture lands in Wave 2; the run itself is real).
	if _, err := py.RunFile(entrypoint, py.FileInput, nil, nil); err != nil {
		return nil, fmt.Errorf("python: run %s: %w", entrypoint, err)
	}
	return rt, nil
}

// Close drops the runtime's references so the captured callables can be GC'd.
// It does NOT finalize the process-global interpreter (other plugins may share
// it). Must hold the GIL to drop *py.Object refs; Decref under a short-lived
// NewLock keeps the OS-thread pin invariant.
func (r *Runtime) Close() {
	if len(r.hooks) == 0 {
		return
	}
	lock := py.NewLock()
	defer lock.Unlock()
	for k, fn := range r.hooks {
		if fn != nil {
			fn.Decref()
		}
		delete(r.hooks, k)
	}
}

// CallHook dispatches an event to the plugin's registered python callable. It
// MUST run on an off-tick pool worker (never the tick goroutine): it acquires
// the GIL on the pinned goroutine, calls the hook with the marshalled scalar
// args, and drops the result reference. A missing hook is a no-op.
func (r *Runtime) CallHook(event string, args ...any) error {
	fn, ok := r.hooks[event]
	if !ok || fn == nil {
		return nil // no python hook registered for this event
	}
	lock := py.NewLock()                      // GIL + LockOSThread for THIS goroutine
	defer lock.Unlock()                       // release GIL + UnlockOSThread
	res, err := fn.Base().CallGoArgs(args...) // Go scalars -> python objects
	if err != nil {
		return fmt.Errorf("python hook %s: %w", event, err)
	}
	if res != nil {
		res.Decref() // gopy returns a New Reference; drop it
	}
	return nil
}
