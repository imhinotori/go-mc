//go:build python

// bridge_python.go is the OPT-IN (`-tags python`) WORLD-BRIDGE python-facing API
// (PLUGIN-06, Plan 26-03): the set_block / spawn / log / block_at builtins handed to
// a runtime="python" plugin's globals at Load, alongside register (register_python.go).
// They are the off-tick REQUEST PRODUCERS: they DO NOT mutate the world — each
// constructs a plain mutation/read request and hands it to the host.WorldBridge
// (the cgo-free seam the server's serverWorldBridge implements), which enqueues it on
// the async rejoin lane for the OWNER to apply through the Phase-23 tick-owned seam
// (the ONLY mutation point). The python side NEVER holds a live tick-owned handle
// (26-RESEARCH Anti-Pattern "Mutating tick-owned state from the off-tick worker" /
// threat T-26-03) — only plain scalars cross the boundary, and the capability gate is
// enforced on the owner at apply.
//
// A READ (block_at) issues a request and BLOCKS on the owner's snapshot reply, then
// returns the COPIED scalar (state id, ok) — request → tick-snapshot → return-a-copy,
// no live world reference off-tick.
//
// gopy citations (VERIFIED, module cache python3.14 source):
//   - py.NewCFunction(name, fn, doc) (*py.CFunction, error)  — METH_VARARGS builtin (cfunction.go)
//   - (*py.Dict).SetItemString(key, val) error               — inject into globals    (dict.go)
//   - (*py.Tuple).Size() / GetIndex(i)                       — read positional args   (tuple.go)
//   - py.AsLong(obj) / (*py.Long).Int64()                    — python int -> Go int   (long.go)
//   - py.NewLong(int64) / py.NewTuple / py.None              — Go scalars -> python    (long.go/tuple.go)

package python

import (
	"fmt"

	"gopython.xyz/py/v14"
)

// SetWorldBridge installs the world-bridge this plugin's off-tick builtins talk to.
// The server calls it once after Load with a bridge stamped with THIS plugin's
// capabilities (server.serverWorldBridge). Stored on the Runtime (read off-tick by
// the builtins); nil leaves the builtins as no-ops. Set on the load goroutine before
// the first dispatch — the lock-free at-load discipline (like hooks).
func (r *Runtime) SetWorldBridge(b host.WorldBridge) { r.bridge = b }

// injectWorldBuiltins adds the world-bridge builtins (set_block/spawn/log/block_at)
// to the plugin's globals dict at Load, alongside register. Each is a Go closure over
// the Runtime so it reaches r.bridge at call time. Called from Load while the GIL is
// held (the dict mutation needs it). A build failure errors the load.
func (r *Runtime) injectWorldBuiltins(globals *py.Dict) error {
	builtins := []struct {
		name string
		fn   func(*py.Tuple) (py.Object, error)
		doc  string
	}{
		{"set_block", r.setBlockBuiltin, "set_block(x, y, z, state): request a world block change (needs world.write)"},
		{"spawn", r.spawnBuiltin, "spawn(x, y, z): request a simple entity spawn (needs entities.write)"},
		{"log", r.logBuiltin, "log(msg): request a log line attributed to this plugin"},
		{"block_at", r.blockAtBuiltin, "block_at(x, y, z) -> (state, ok): read a block state id (needs world.read)"},
	}
	for _, b := range builtins {
		fn, err := py.NewCFunction(b.name, b.fn, b.doc)
		if err != nil {
			return fmt.Errorf("python: build %s builtin: %w", b.name, err)
		}
		defer fn.Decref()
		if err := globals.SetItemString(b.name, fn); err != nil {
			return fmt.Errorf("python: inject %s builtin: %w", b.name, err)
		}
	}
	return nil
}

// pyInt extracts a Go int from a python int arg at tuple index i, returning a
// python-visible error for a non-int (the request producers take plain ints).
func pyInt(args *py.Tuple, i int) (int, error) {
	o, err := args.GetIndex(i)
	if err != nil {
		return 0, fmt.Errorf("arg %d: %w", i, err)
	}
	l, ok := o.(*py.Long)
	if !ok {
		return 0, fmt.Errorf("arg %d must be an int", i)
	}
	return int(l.Int64()), nil
}

// setBlockBuiltin is the python set_block(x,y,z,state) — it CONSTRUCTS a request via
// the bridge (no mutation here). A missing bridge (no world lane) is a no-op. The
// capability gate is enforced on the OWNER at apply (a denied write is dropped +
// logged there), so this producer is unconditional.
func (r *Runtime) setBlockBuiltin(args *py.Tuple) (py.Object, error) {
	if args == nil || args.Size() != 4 {
		return nil, fmt.Errorf("set_block: want exactly 4 args (x, y, z, state), got %d", argsLen(args))
	}
	if r.bridge == nil {
		return py.None, nil // no world lane wired: the request producer is a no-op
	}
	x, err := pyInt(args, 0)
	if err != nil {
		return nil, fmt.Errorf("set_block: %w", err)
	}
	y, err := pyInt(args, 1)
	if err != nil {
		return nil, fmt.Errorf("set_block: %w", err)
	}
	z, err := pyInt(args, 2)
	if err != nil {
		return nil, fmt.Errorf("set_block: %w", err)
	}
	state, err := pyInt(args, 3)
	if err != nil {
		return nil, fmt.Errorf("set_block: %w", err)
	}
	r.bridge.SetBlock(x, y, z, state) // enqueue the request; the owner applies it on the tick
	return py.None, nil
}

// spawnBuiltin is the python spawn(x,y,z) — constructs a spawn request via the bridge.
func (r *Runtime) spawnBuiltin(args *py.Tuple) (py.Object, error) {
	if args == nil || args.Size() != 3 {
		return nil, fmt.Errorf("spawn: want exactly 3 args (x, y, z), got %d", argsLen(args))
	}
	if r.bridge == nil {
		return py.None, nil
	}
	x, err := pyInt(args, 0)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	y, err := pyInt(args, 1)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	z, err := pyInt(args, 2)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	r.bridge.Spawn(x, y, z)
	return py.None, nil
}

// logBuiltin is the python log(msg) — constructs a log request via the bridge.
func (r *Runtime) logBuiltin(args *py.Tuple) (py.Object, error) {
	if args == nil || args.Size() != 1 {
		return nil, fmt.Errorf("log: want exactly 1 arg (msg), got %d", argsLen(args))
	}
	if r.bridge == nil {
		return py.None, nil
	}
	o, err := args.GetIndex(0)
	if err != nil {
		return nil, fmt.Errorf("log: read msg: %w", err)
	}
	msg, ok := pyString(o)
	if !ok {
		return nil, fmt.Errorf("log: msg must be a string")
	}
	r.bridge.Log(msg)
	return py.None, nil
}

// blockAtBuiltin is the python block_at(x,y,z) -> (state, ok) READ. It issues a read
// request through the bridge, which BLOCKS on the owner's snapshot reply and returns
// the COPIED scalar — request → tick-snapshot → return-a-copy (no live handle
// off-tick). A missing bridge returns (0, False). The capability gate (world.read) is
// enforced on the owner at apply (a denied read returns ok=False there).
func (r *Runtime) blockAtBuiltin(args *py.Tuple) (py.Object, error) {
	if args == nil || args.Size() != 3 {
		return nil, fmt.Errorf("block_at: want exactly 3 args (x, y, z), got %d", argsLen(args))
	}
	state, ok := 0, false
	if r.bridge != nil {
		x, err := pyInt(args, 0)
		if err != nil {
			return nil, fmt.Errorf("block_at: %w", err)
		}
		y, err := pyInt(args, 1)
		if err != nil {
			return nil, fmt.Errorf("block_at: %w", err)
		}
		z, err := pyInt(args, 2)
		if err != nil {
			return nil, fmt.Errorf("block_at: %w", err)
		}
		state, ok = r.bridge.BlockAt(x, y, z) // request → owner snapshot → copy back
	}
	// Marshal (state, ok) back as a python 2-tuple of plain scalars. PackTuple uses
	// SetIndexSteal — it STEALS the item references (consumes them), so we must NOT
	// Decref stateObj after (that would over-decref / double-free). py.True/py.False
	// are singletons with no-op Decref/Incref, so stealing them is harmless.
	stateObj := py.NewLong(int64(state)) // New Reference, stolen by PackTuple below
	var okObj py.Object = py.False
	if ok {
		okObj = py.True
	}
	tup, err := py.PackTuple(stateObj, okObj)
	if err != nil {
		// PackTuple failed BEFORE stealing stateObj (NewTuple/SetIndexSteal error):
		// drop the New Reference we still own so it does not leak.
		stateObj.Decref()
		return nil, fmt.Errorf("block_at: pack result: %w", err)
	}
	return tup, nil
}
