package host

import (
	"log"

	starlarkpkg "github.com/imhinotori/sulfur/plugin/starlark"
	"go.starlark.net/starlark"
)

// Emit dispatches a discrete gameplay event to every subscribed hook. The
// server calls it at the discrete seams (a block breaks, a mob dies) on the
// tick goroutine.
//
// Zero-subscriber fast path (Pitfall 6): the len-check happens BEFORE
// payload.toStarlark(), so an unsubscribed event costs a single map read with
// ZERO allocation. This is what makes on_tick (and every hot funnel) free on a
// server with no plugin loaded.
//
// Per-hook isolation (Pitfall 5 / T-22-02): each hook runs on a FRESH thread
// (Pitfall 4 — never share a Thread) inside a recover, so one hook that errors
// OR panics is logged and skipped — it never aborts the remaining hooks or
// kills the tick. The fresh thread carries the Phase-21 step budget (T-22-01),
// so a runaway hook returns *EvalError and the loop continues.
func (m *Manager) Emit(evt EventType, payload Event) {
	hooks := m.hooks[evt]
	if len(hooks) == 0 {
		return // NO subscribers → one map read, zero interpreter cost
	}
	args := payload.toStarlark()
	for _, h := range hooks {
		m.callHook(evt, h, args)
	}
}

// callHook runs a single hook with per-call panic+error isolation on a fresh,
// budget-bounded thread.
func (m *Manager) callHook(evt EventType, h Hook, args starlark.Tuple) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("plugin %q hook %s panicked: %v", h.plugin, evt, r)
		}
	}()
	th := starlarkpkg.NewThread("emit:" + string(evt))
	if _, err := starlark.Call(th, h.fn, args, nil); err != nil {
		log.Printf("plugin %q hook %s error: %v", h.plugin, evt, err)
	}
}
