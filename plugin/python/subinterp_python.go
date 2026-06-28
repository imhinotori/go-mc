//go:build python

// subinterp_python.go records the GIL / sub-interpreter execution DECISION for
// the off-tick python lane (26-CONTEXT decision 5 "GIL: sub-interpreters —
// operator-directed, FALL BACK if not cleanly usable, do NOT fake parallelism").
//
// ====================================================================
// DECISION: ONE process-global interpreter + SERIALIZED calls (the safe
// fallback). Sub-interpreters are NOT used. This is NOT faked parallelism — the
// off-tick lane runs python OFF the tick goroutine on the ants pool, but every
// CallHook serializes through the single process-global GIL.
// ====================================================================
//
// WHY THE FALLBACK (cited evidence — gopy @ python3.14 source, read from the
// module cache at gopython.xyz/py/v14@...-b0bdc04a384b, the pinned commit):
//
//  1. gopy's GIL surface is the PyGILState API, which is single-interpreter by
//     construction. lock.go uses ONLY:
//       - C.PyGILState_Ensure() / C.PyGILState_Release()  (GILState.Ensure/Release)
//       - py.InitAndLock() -> InitializeEx + PyEval_SaveThread  (process-global Py_Initialize)
//       - Lock.Finalize() -> C.Py_Finalize()                    (process-global teardown)
//     PyGILState_Ensure is explicitly documented by CPython as assuming a SINGLE
//     (the main) interpreter; it is incompatible with per-sub-interpreter thread
//     states. gopy hangs its entire GIL model on it.
//
//  2. gopy exposes NO sub-interpreter creation surface. A full source grep of the
//     pinned module (`grep -rniE "NewInterpreter|sub.?interp|InterpreterConfig|
//     Py_NewInterpreter|EndInterpreter|PyInterpreterConfig"`) returns ZERO hits in
//     any non-test .go file. There is no Py_NewInterpreterFromConfig binding, no
//     PyInterpreterConfig struct, and no per-interpreter-GIL (PEP 684) config knob.
//     PEP 734 isolated interpreters (concurrent.interpreters) would have to be
//     driven from python code, not from gopy's Go API, and gopy's PyGILState lock
//     model would still serialize the embedding boundary.
//
//  3. The gopy version is an ALPHA (v14.0.0-alpha.0...). Per 26-CONTEXT decision 5,
//     an unstable/missing surface is exactly the "not cleanly usable in this alpha"
//     condition that mandates the serialized fallback with the WHY cited — rather
//     than building a fragile, possibly-corrupting sub-interpreter path on an alpha.
//
// CONSEQUENCE (and why the gate still passes): with one interpreter + one GIL,
// pool workers cannot run python truly in parallel — whoever holds the GIL blocks
// the rest (26-RESEARCH Pitfall 3). We therefore SIZE THE POOL SMALL
// (server uses asyncSmallPoolSize for the pluginPool) and accept serialization:
// the lane is for HEAVY-but-not-necessarily-concurrent off-tick plugins, and the
// drop-on-overload bound (submitOrDrop) caps the damage. The PLUGIN-06 gate
// requires python OFF-TICK + CGO=0-default, NOT N-way parallelism (26-CONTEXT
// decision 5 "the gate passes either way") — so this fallback satisfies it.
//
// REVISIT WHEN: gopy ships a stable Py_NewInterpreterFromConfig / per-interpreter
// GIL (PEP 684) binding, OR we move to the free-threaded (PEP 703) 3.14 build.
// Then one isolated sub-interpreter per worker becomes the throughput upgrade —
// implemented HERE, behind this same tag, with the pool re-sized to NumCPU.

package python

// serialized is the compile-time record of the chosen execution mode. It exists
// so the tagged test (offtick_python_test.go) can assert the selected mode is
// internally consistent (serialized calls through the single GIL, no corruption)
// rather than asserting a parallelism that — per the cited evidence above — gopy
// @3.14 does not cleanly provide. true == one interpreter + serialized calls.
const serialized = true

// subInterpreters reports whether per-worker sub-interpreters are in use. It is
// false: gopy@3.14 does not cleanly expose sub-interpreter creation / the
// per-interpreter GIL (see the cited evidence above), so the lane runs one
// serialized interpreter. Exposed for the tagged test + operator introspection.
func subInterpreters() bool { return !serialized }
