//go:build !python

// runtime_stub.go is the DEFAULT-build (no `-tags python`) face of the python
// package. It is cgo-free: it imports NO gopy and contains NO `import "C"`, so
// the default `CGO_ENABLED=0 go build ./...` stays pure-Go static and the
// default import graph never reaches libpython. Every exported symbol here MUST
// match runtime_python.go's signature exactly (RESEARCH Pitfall 7 — stub/impl
// drift breaks one of the two builds); the bodies just refuse with ErrNotBuilt.

package python

import "errors"

// ErrNotBuilt is returned by every entry point when the binary was built WITHOUT
// `-tags python`. The host's runtime-routing branch turns this into a graceful
// "python runtime not built in this binary; skipping" log + continue.
var ErrNotBuilt = errors.New("python: runtime not built in this binary (rebuild with -tags python)")

// Available reports whether the CPython runtime is compiled into this binary.
// On the default (no-tag) build it is always false.
func Available() bool { return false }

// Runtime is the opaque per-plugin handle. On the stub side it carries nothing.
type Runtime struct{}

// Load is the no-tag stub: it refuses, so a runtime="python" plugin is skipped
// gracefully on a default build instead of loading.
func Load(entrypoint string) (*Runtime, error) { return nil, ErrNotBuilt }

// Close is a no-op on the stub (there is no interpreter to tear down).
func (r *Runtime) Close() {}

// CallHook refuses on the stub: there is no CPython interpreter to dispatch to.
func (r *Runtime) CallHook(event string, args ...any) error { return ErrNotBuilt }
