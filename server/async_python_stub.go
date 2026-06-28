//go:build !python

// async_python_stub.go is the DEFAULT-build (no `-tags python`) twin of
// async_python_python.go. It is cgo-free: it imports NO plugin/python tagged side
// and NO gopy, so `CGO_ENABLED=0 go build ./...` stays pure-Go static with ZERO
// gopython in the import graph (THE #1 gate). WirePython is a no-op here — no
// python runtime is registered, so host.LoadDir skips runtime="python" manifests
// gracefully (logs "python runtime not built in this binary") and host.Emit's
// python dispatch (nil callback) is a cheap no-op. The off-tick lane plumbing
// (pythonHookReady, submitPythonHook, t.pluginPool) is STILL default-built (it is
// cgo-free, in async_python.go + tick.go) — only the gopy WIRING is gated.

package server

import "github.com/imhinotori/sulfur/plugin/host"

// WirePython is a no-op on the default build: no CPython runtime is compiled in,
// so nothing is registered. A runtime="python" plugin is skipped gracefully by
// LoadDir, and Emit's python dispatch stays nil (cheap no-op). The signature
// matches the tagged twin so main calls it unconditionally.
func WirePython(t *TickLoop, m *host.Manager) {
	_ = t
	_ = m
}
