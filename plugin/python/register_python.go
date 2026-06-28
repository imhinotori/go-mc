//go:build python

// register_python.go is the OPT-IN (`-tags python`) register-capture: the SAME
// register(event, fn) concept Starlark plugins use (plugin/host/register.go),
// re-expressed for the CPython lane. The host already routes by manifest.Runtime
// (26-01) — a runtime="python" plugin uses the IDENTICAL register API; only the
// execution lane differs (Starlark inline-on-tick, Python off-tick). This file
// fleshes out the Wave-2 register-capture stub 26-01 left in Load.
//
// Mechanism (VERIFIED against the gopy python3.14 source in the module cache):
// Load builds a globals Dict carrying a `register` CFunction (a Go closure with
// the METH_VARARGS signature func(*py.Tuple) (py.Object, error) — cfunction.go),
// then RunFile(entrypoint, FileInput, globals, globals). When the .py calls
// register("on_block_break", fn) at module top-level, the closure captures the
// (event, callable) pair into rt.hooks, Incref'ing the callable so it survives the
// module body returning. This is the register-ONCE-at-load discipline — the hooks
// map is WRITTEN only here (on the load goroutine, before the off-tick lane runs)
// and READ off-tick by CallHook; no lock needed (the load completes before the
// first dispatch, mirroring the host's lock-free hooks map).

package python

import (
	"fmt"

	"gopython.xyz/py/v14"
)

// Load initializes CPython ONCE (process-global), injects the register(event, fn)
// builtin, runs the plugin's entrypoint .py (whose top-level register(...) calls
// capture its hooks into rt.hooks), and returns the Runtime holding the captured
// callables. It runs on the host goroutine before the tick loop, holding the GIL
// only for the duration of the run.
func Load(entrypoint string) (*Runtime, error) {
	ensureInit()

	// Acquire the GIL for THIS goroutine to build globals + run the plugin body.
	lock := py.NewLock()
	defer lock.Unlock()

	rt := &Runtime{hooks: map[string]py.Object{}}

	// Build the plugin's globals dict and inject the register builtin. RunFile with
	// a non-nil globals runs the module body against THIS dict, so register(...) at
	// the top level resolves to our closure.
	globals, err := py.NewDict()
	if err != nil {
		return nil, fmt.Errorf("python: new globals for %s: %w", entrypoint, err)
	}
	// Retain globals on the Runtime (dropped in Close) so module-level plugin state
	// (e.g. an aggregation counter the hook mutates) outlives Load; the hooks'
	// __globals__ reference it regardless, but holding it makes the lifetime explicit
	// and lets the tagged tests read a plugin global back.
	rt.globals = globals

	reg, err := py.NewCFunction("register", rt.registerBuiltin, "register(event, fn): subscribe a hook")
	if err != nil {
		return nil, fmt.Errorf("python: build register builtin for %s: %w", entrypoint, err)
	}
	defer reg.Decref()
	if err := globals.SetItemString("register", reg); err != nil {
		return nil, fmt.Errorf("python: inject register for %s: %w", entrypoint, err)
	}

	// Run the plugin entrypoint against the injected globals. Its top-level
	// register(event, fn) calls populate rt.hooks via registerBuiltin below.
	res, err := py.RunFile(entrypoint, py.FileInput, globals, globals)
	if err != nil {
		return nil, fmt.Errorf("python: run %s: %w", entrypoint, err)
	}
	if res != nil {
		res.Decref() // RunFile returns a New Reference; drop it
	}
	return rt, nil
}

// registerBuiltin is the Go implementation of the python register(event, fn)
// builtin (METH_VARARGS: func(*py.Tuple) (py.Object, error), per cfunction.go). It
// captures (event -> callable) into rt.hooks, Incref'ing the callable so it
// outlives the module body. Called on the load goroutine while the GIL is held
// (Load holds it). A wrong arg count / non-string event is a python-visible
// error, matching the host register's load-time validation discipline.
func (r *Runtime) registerBuiltin(args *py.Tuple) (py.Object, error) {
	if args == nil || args.Size() != 2 {
		return nil, fmt.Errorf("register: want exactly 2 args (event, fn), got %d", argsLen(args))
	}
	evtObj, err := args.GetIndex(0)
	if err != nil {
		return nil, fmt.Errorf("register: read event arg: %w", err)
	}
	fn, err := args.GetIndex(1)
	if err != nil {
		return nil, fmt.Errorf("register: read fn arg: %w", err)
	}

	event, ok := pyString(evtObj)
	if !ok {
		return nil, fmt.Errorf("register: event must be a string")
	}
	if fn == nil {
		return nil, fmt.Errorf("register: fn for %q is None", event)
	}

	// Incref the callable so it survives the module body returning and globals being
	// dropped; Close Decref's it. Overwrite-and-Decref the old hook if the plugin
	// re-registers the same event (last registration wins, like the host bus would).
	fn.Incref()
	if old, exists := r.hooks[event]; exists && old != nil {
		old.Decref()
	}
	r.hooks[event] = fn

	return py.None, nil
}

// argsLen returns the tuple size or 0 for a nil tuple (for the error message).
func argsLen(args *py.Tuple) int {
	if args == nil {
		return 0
	}
	return args.Size()
}

// pyString extracts a Go string from a python str object, returning false if it
// is not a unicode/str. It is the event-name marshal for register.
func pyString(o py.Object) (string, bool) {
	u, ok := o.(*py.Unicode)
	if !ok {
		return "", false
	}
	s, err := u.AsString()
	if err != nil {
		return "", false
	}
	return s, true
}
