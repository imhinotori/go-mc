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

// makeChatBuiltin builds the `chat(msg)` host builtin (Plan 28-02): it unpacks one
// string and, if the server installed a chat sink (SetChatSink), hands the message to
// it so the reaction lands on the player-facing wire as a ClientboundSystemChat — the
// observable proof that a plugin event hook fired (gate checklist item #4). When NO sink
// is installed (a unit test, or a server with the chat lane off), it FALLS BACK to log()
// so the reaction is still recorded and the host stays self-contained.
//
// It is METHOD-BOUND on the Manager (like register/set_recipe_matcher) so it can read
// m.chatSink — the same lock-free discipline: the sink is written once at boot and read
// here on the tick goroutine inside Emit (TICK-05). It touches NO entity/world handle and
// cannot bypass a capability — the sandbox surface widens by exactly one server-controlled
// text-output seam (T-28-08).
func (m *Manager) makeChatBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("chat", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var msg string
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &msg); err != nil {
			return nil, err
		}
		if m.chatSink != nil {
			m.chatSink(msg)
		} else {
			// No server sink installed — keep the reaction observable + the host
			// self-contained (the chat() fallback-to-log keeps CGO=0 ./... green when
			// no sink is wired, e.g. a host-package unit test).
			log.Printf("plugin: %s", msg)
		}
		return starlark.None, nil
	})
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
