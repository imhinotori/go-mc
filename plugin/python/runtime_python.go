//go:build python

// runtime_python.go is the OPT-IN (`-tags python`) face of the python package.
// It imports gopy (`gopython.xyz/py/v14`), which carries `import "C"` +
// `#cgo pkg-config: python-3.14-embed libffi`, so compiling THIS file forces cgo
// and links libpython 3.14. It NEVER compiles on the default build (the `python`
// tag excludes it), which is exactly what keeps `CGO_ENABLED=0 go build ./...`
// pure-Go static. The exported surface mirrors runtime_stub.go byte-for-byte
// (Available/Runtime/Load/Close/CallHook) so the package builds in both modes.
//
// This file owns INIT + LIFECYCLE. The register-capture (the register(event, fn)
// builtin that harvests the .py's hooks) lives in register_python.go; the GIL-held
// off-tick dispatch (CallHook) lives in dispatch_python.go; the GIL/interpreter
// concurrency decision (one serialized interpreter — sub-interpreters are NOT
// cleanly exposed by gopy@3.14, cited) lives in subinterp_python.go. All four are
// `//go:build python`.
//
// gopy API citations (VERIFIED against the python3.14 branch source in the module
// cache, 26-RESEARCH.md "Standard Stack" + "Code Examples"):
//   - py.InitAndLock() *py.Lock     — Py_Initialize + GIL + LockOSThread, returns LOCKED  (lock.go)
//   - py.NewLock() *py.Lock         — GIL acquire + LockOSThread pin for THIS goroutine    (lock.go)
//   - (*py.Lock).Lock()/Unlock()    — re-acquire / release GIL + UnlockOSThread            (lock.go)
//   - py.RunFile(path, py.FileInput, globals, locals) (py.Object, error)                   (run.go)
//   - py.NewDict() (*py.Dict, error) + (*Dict).SetItemString                               (dict.go)
//   - py.NewCFunction(name, fn, doc) (*py.CFunction, error)                                (cfunction.go)
//   - (py.Object).Base().CallGoArgs(args ...any) (py.Object, error)                        (base.go)
//   - (py.Object).Incref()/Decref() — manage the New/Borrowed reference gopy returns       (baseobject_gen.go)
//
// Off-tick discipline (PLUGIN-06): Load runs on the host goroutine before the
// tick loop; CallHook runs ONLY on an off-tick ants pool worker (never tickOnce),
// holding the GIL on the LockOSThread-pinned worker.

package python

import (
	"sync"

	"gopython.xyz/py/v14"
)

// initOnce guards the process-global Py_Initialize: gopy embeds ONE CPython
// interpreter per process (sub-interpreters are not cleanly usable via gopy@3.14
// — see subinterp_python.go), so the first Load initializes + releases the
// bootstrap Lock; subsequent Loads re-acquire the GIL instead of re-initializing.
var initOnce sync.Once

// Available reports whether the CPython runtime is compiled in — true on the
// `-tags python` build.
func Available() bool { return true }

// Runtime owns the hooks a single plugin registered at load. The callables are
// py.Object references (New References captured + Incref'd by the injected
// register(event, fn) builtin in register_python.go) and dispatched OFF-TICK by
// CallHook (dispatch_python.go). The single process-global GIL serializes every
// CallHook across all Runtimes (subinterp_python.go documents why there is no
// per-Runtime sub-interpreter).
type Runtime struct {
	hooks map[string]py.Object // event name -> the registered python callable (a held reference)
}

// ensureInit initializes CPython exactly once for the process. InitAndLock
// returns a LOCKED bootstrap Lock; we release it after init so off-tick workers
// can acquire the GIL via NewLock. Safe to call from every Load (sync.Once).
func ensureInit() {
	initOnce.Do(func() {
		boot := py.InitAndLock() // Py_Initialize + GIL + LockOSThread (LOCKED)
		boot.Unlock()            // release the GIL after one-time setup
	})
}

// Close drops the runtime's references so the captured callables can be GC'd by
// CPython. It does NOT finalize the process-global interpreter (other plugins may
// share it). Must hold the GIL to drop references; Decref under a short-lived
// NewLock keeps the OS-thread pin invariant (gopy Lock == runtime.LockOSThread).
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
