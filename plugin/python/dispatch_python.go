//go:build python

// dispatch_python.go is the OPT-IN (`-tags python`) off-tick dispatch: the
// GIL-held call into a plugin's registered python hook. It MUST run on an ants
// pool worker (server/async_python_python.go submits it via submitOrDrop), NEVER
// on the tick goroutine (PLUGIN-06 off-tick rule; 26-RESEARCH Pitfall 2).
//
// The single process-global GIL serializes every CallHook (subinterp_python.go
// documents why there is no per-worker sub-interpreter in gopy@3.14). The pool is
// sized small (asyncSmallPoolSize) so the serialized lane cannot pile work; a
// saturated pool DROPS (submitOrDrop), the tick never blocks (T-26-07).
//
// gopy citations (VERIFIED, module cache python3.14 source):
//   - py.NewLock() *py.Lock         — GIL acquire + runtime.LockOSThread for THIS goroutine (lock.go)
//   - (*py.Lock).Unlock()           — release GIL + runtime.UnlockOSThread                  (lock.go)
//   - (py.Object).Base().CallGoArgs(args ...any) (py.Object, error) — Go scalars -> py args (base.go)
//   - (py.Object).Decref()          — drop the New Reference CallGoArgs returns              (baseobject_gen.go)

package python

import (
	"fmt"

	"gopython.xyz/py/v14"
)

// CallHook dispatches an event to the plugin's registered python callable. It
// MUST run on an off-tick pool worker (never the tick goroutine): it acquires
// the GIL on the pinned goroutine, calls the hook with the marshalled scalar
// args, and drops the result reference. A missing hook is a no-op (nil error).
//
// The whole lock->call->Decref->unlock is one unit on one goroutine: gopy's
// NewLock() calls runtime.LockOSThread() (CPython per-thread state), and there is
// NO Go channel/blocking op between Lock and Unlock (26-RESEARCH Pitfall 5 — no
// OS-thread migration mid-call). The result rejoin happens AFTER Unlock, back in
// the server submit shim (it sends a plain-value pythonHookReady on asyncIn2).
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
