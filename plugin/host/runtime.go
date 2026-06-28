package host

// runtime.go is the runtime-routing SEAM that lets LoadDir dispatch a
// runtime="python" plugin to the opt-in CPython lane WITHOUT plugin/host ever
// importing gopy. These are plain Go interfaces (ZERO cgo): the host holds a
// PythonRuntime by interface; the CONCRETE gopy-backed implementation is
// registered from a //go:build python file at boot (via SetPythonRuntime), so
// the default (no-tag) build registers nothing → the interface is nil → a
// python manifest is skipped gracefully. This is how server + plugin/host stay
// cgo-free on the default build (26-CONTEXT decision 6, 26-RESEARCH "keep the
// host cgo-free").

// PythonRuntime is the minimal surface the host needs to load a python plugin.
// The concrete impl wraps plugin/python.Load behind //go:build python; on the
// default build no runtime is registered and the interface is nil.
type PythonRuntime interface {
	// Available reports whether the CPython runtime is compiled in (the
	// -tags python build). A registered-but-unavailable runtime is treated the
	// same as "not built in".
	Available() bool
	// Load loads a python plugin from its entrypoint path and returns a handle
	// the off-tick lane (Wave 2) dispatches events to.
	Load(entrypoint string) (PythonPlugin, error)
}

// PythonPlugin is a loaded python plugin handle. It is stored on loadedPlugin so
// the Wave-2 off-tick dispatch can call its hooks; Phase-26 Plan 01 only needs
// it to plug into the routing seam. It is a plain interface — no cgo, no
// *py.Object crosses into the host.
type PythonPlugin interface {
	// CallHook dispatches an event to the plugin OFF-TICK (never on the tick
	// goroutine). Wave 2 wires the submit site.
	CallHook(event string, args ...any) error
	// SetWorldBridge installs the WORLD-BRIDGE (Plan 26-03) the plugin's off-tick
	// set_block/spawn/log/block_at builtins talk to. The host calls it once at load
	// with a bridge stamped with THIS plugin's capabilities; a nil bridge (no world
	// lane wired) leaves the builtins as request producers with nowhere to send,
	// which they handle as a no-op. It is a plain-Go interface (no cgo, no
	// *py.Object) so plugin/host stays cgo-free.
	SetWorldBridge(b WorldBridge)
	// Close tears the plugin's references down (on unload).
	Close()
}

// WorldBridge is the plain-Go (cgo-free) seam the off-tick python world builtins
// (set_block/spawn/log/block_at) call to reach the tick-owned world (Plan 26-03).
// The CONCRETE impl lives in server (serverWorldBridge): a write CONSTRUCTS a plain
// mutation request + enqueues it on the async rejoin lane (the owner applies it
// through the Phase-23 seam, the ONLY mutation point), and a read issues a request →
// owner-snapshot → return-a-copy. NO live tick-owned handle ever crosses this
// interface — only plain scalars + the request/apply indirection (the safety
// boundary, 26-CONTEXT decision 4). The host holds it BY INTERFACE so plugin/host
// and plugin/python never import server (no cycle, no cgo).
type WorldBridge interface {
	// SetBlock requests a world block change (capWorldWrite, enforced on apply).
	SetBlock(x, y, z, state int)
	// Spawn requests a simple entity spawn (capEntitiesWrite, enforced on apply).
	Spawn(x, y, z int)
	// Log requests a log line attributed to the plugin (no capability).
	Log(msg string)
	// BlockAt reads a block state id (capWorldRead). It BLOCKS on the owner's
	// snapshot reply and returns the COPIED scalar (state id, ok) — never a live
	// reference.
	BlockAt(x, y, z int) (int, bool)
}

// SetPythonRuntime registers the concrete python runtime. It is called at boot
// from a //go:build python file (the tagged adapter), so plugin/host itself
// never imports plugin/python's tagged side. On the default build nobody calls
// it → m.pythonRuntime stays nil → LoadDir skips python manifests gracefully.
func (m *Manager) SetPythonRuntime(rt PythonRuntime) { m.pythonRuntime = rt }

// pythonAvailable reports whether a usable python runtime is registered.
func (m *Manager) pythonAvailable() bool {
	return m.pythonRuntime != nil && m.pythonRuntime.Available()
}
