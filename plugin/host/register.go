package host

import (
	"fmt"

	"go.starlark.net/starlark"
)

// makeRegisterBuiltin builds the `register(event_name, fn)` Go builtin the host
// injects into a plugin's predeclared globals. When the plugin's module body
// runs ONCE at load, each register(...) call captures the passed callable into
// m.hooks keyed by event. The captured starlark.Callable is frozen after Load
// returns, so it is safe to invoke from the tick goroutine on a fresh thread.
//
// An unknown/typo'd event name errors at LOAD (not silently at runtime) — a
// hook that never fires with no error is the worst debugging experience
// (Pitfall 3).
func (m *Manager) makeRegisterBuiltin(pluginName string) *starlark.Builtin {
	return starlark.NewBuiltin("register", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var fn starlark.Callable // any def/lambda/builtin satisfies Callable
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 2, &name, &fn); err != nil {
			return nil, err
		}
		evt := EventType(name)
		if !isKnownEvent(evt) {
			return nil, fmt.Errorf("register: unknown event %q", name)
		}
		m.hooks[evt] = append(m.hooks[evt], Hook{fn: fn, plugin: pluginName})
		return starlark.None, nil
	})
}
