package server

// async_python.go is the OFF-TICK PYTHON LANE rejoin + submit (PLUGIN-06, Plan
// 26-02) — the pathReady discipline (async.go) applied to plugin dispatch. It is
// CGO-FREE and compiles in BOTH builds: it carries only plain values and talks to
// the python runtime through host.PythonPlugin (the plain-Go interface from
// 26-01), never gopy. The gopy call is confined to plugin/python behind
// //go:build python — it runs INSIDE host.PythonPlugin.CallHook, one interface
// hop away, so this file (and the whole default `server` graph) stays pure-Go
// static with ZERO gopython in the import graph (THE #1 gate).
//
// THE LANE (mirrors pathReady exactly, 26-CONTEXT decision 3):
//   discrete event (tick goroutine) → host.Emit → host.emitPython → submitPythonHook
//     → submitOrDrop(t.pluginPool, ...) [the existing bounded ants drop-on-overload]
//       → off-tick worker: CallHook (GIL-held, LockOSThread-pinned, in plugin/python)
//         → t.asyncIn2 <- pythonHookReady{...} [the rejoin channel]
//           → applyAsyncResults drains it on the OWNER → pythonHookReady.applyTo
// The python hook NEVER runs on the tick goroutine; the ONLY tick-state touch is
// applyTo on the owner (TICK-05), exactly like pathReady/trackerDiffReady.

import "github.com/imhinotori/sulfur/plugin/host"

// pythonHookReady is the OFF-TICK python rejoin message — the pathReady twin for
// plugin dispatch. It carries ONLY plain scalars (plugin/event/note), NEVER a
// *py.Object or a live tick pointer (the pathReady cardinal rule, async.go), so a
// result tolerated 1+ ticks late is safe to apply on the owner. It satisfies the
// EXISTING asyncResult interface{ applyTo(*TickLoop) } (tick.go, UNCHANGED).
type pythonHookReady struct {
	plugin string // attribution / re-validate on apply (the plugin may have been unloaded)
	event  string // which discrete event this dispatch was for
	note   string // a marshalled-out Go scalar outcome ("ok" or the hook error string)
}

// applyTo runs on the OWNER goroutine inside applyAsyncResults — the Plan-26-02
// implementation of the Wave-0 asyncResult contract. The off-tick worker ran the
// python hook (GIL-held) and handed back this plain-value outcome; the owner does
// the MINIMAL owner-side effect. Phase-26 owner-side effect is bookkeeping ONLY
// (telemetry counter / error log) — NOT world mutation (that is Plan 26-03's
// request→apply bridge). It mirrors pathReady.applyTo's re-validate-then-apply
// discipline: the plugin may have been unloaded between submit and apply, in which
// case there is simply nothing to mutate (Phase-26 keeps no per-plugin owner state
// to drop, so the bookkeeping is unconditional + harmless).
func (r pythonHookReady) applyTo(t *TickLoop) {
	if r.note != "ok" {
		// The off-tick python hook errored; surface it owner-side (the worker must
		// NOT log/mutate tick-owned telemetry off-tick — only the owner does, TICK-05).
		t.pythonHookErrors++
		return
	}
	t.pythonHooksApplied++
}

// submitPythonHook submits a python plugin hook to the off-tick plugin pool with
// the EXISTING drop-on-overload discipline (submitOrDrop, async.go), then the
// worker rejoins via pythonHookReady on asyncIn2. It is CGO-FREE: pp is the plain
// host.PythonPlugin interface; the gopy call is inside pp.CallHook (plugin/python,
// behind the tag). This is the callback the server registers on the host via
// SetPythonDispatch — host.emitPython invokes it on the tick goroutine, it SUBMITS
// (non-blocking) and returns immediately so the tick never blocks on python
// (T-26-07). A saturated pool DROPS the dispatch (sized small, asyncSmallPoolSize).
func (t *TickLoop) submitPythonHook(pp host.PythonPlugin, event string, args []any) {
	if pp == nil {
		return
	}
	// Capture the plugin name is not available here (the host passes the handle, not
	// the manifest name); the note carries attribution enough for Phase-26 telemetry.
	submitOrDrop(t.pluginPool, func() {
		// OFF-TICK: CallHook acquires the GIL on this LockOSThread-pinned worker,
		// runs the python hook, releases the GIL — all inside CallHook (plugin/python).
		err := pp.CallHook(event, args...)
		note := "ok"
		if err != nil {
			note = err.Error()
		}
		// Rejoin on the OWNER channel (the pathReady discipline). The send is on the
		// bounded asyncIn2 (cap asyncIn2Buffer); applyAsyncResults drains it on the
		// owner and runs applyTo there — the ONLY tick-state touch.
		t.asyncIn2 <- pythonHookReady{event: event, note: note}
	})
}
