package starlark

import (
	"fmt"

	"go.starlark.net/starlark"
)

// LoadedPlugin holds a plugin's auto-frozen module globals. The globals are
// immutable after load, so reading them is race-free from any goroutine; the
// caller must still use a FRESH Thread for each Call (Threads never cross
// goroutines).
type LoadedPlugin struct {
	path    string
	globals starlark.StringDict
}

// Global returns a frozen module global by name (read-only; safe from any
// goroutine).
func (p *LoadedPlugin) Global(name string) (starlark.Value, bool) {
	v, ok := p.globals[name]
	return v, ok
}

// Call invokes a Starlark function global by name on a FRESH per-call Thread
// (one Thread per goroutine; the budget is enforced). The fn value is frozen
// and safe to read; only the Thread is goroutine-local.
func (p *LoadedPlugin) Call(name string, args ...starlark.Value) (starlark.Value, error) {
	fn, ok := p.globals[name]
	if !ok {
		return nil, fmt.Errorf("starlark: %s: no global %q", p.path, name)
	}
	th := newThread(p.path + ":" + name)
	return starlark.Call(th, fn, starlark.Tuple(args), nil)
}
