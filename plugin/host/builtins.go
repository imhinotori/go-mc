package host

import (
	"log"

	"go.starlark.net/starlark"
)

// logBuiltin is a server-neutral `log(msg)` builtin for plugins: it formats its
// single string argument and writes it to the standard logger. It touches NO
// game state — like Phase-21's echo, it is part of the narrow Phase-22 surface
// (the real entity/world handles are Phase 23). Fixtures and example plugins
// use it to make a hook's execution observable.
func logBuiltin(th *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msg string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &msg); err != nil {
		return nil, err
	}
	log.Printf("plugin: %s", msg)
	return starlark.None, nil
}

// hostBuiltins is the set of host-provided builtins injected on top of the
// Starlark sandbox allowlist for every plugin load. `register` is added
// per-plugin (it captures the plugin name) in LoadDir; the builtins here are
// plugin-name-independent.
func hostBuiltins() starlark.StringDict {
	return starlark.StringDict{
		"log": starlark.NewBuiltin("log", logBuiltin),
	}
}
