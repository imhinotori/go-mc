// Package starlark embeds the go.starlark.net interpreter as a sandboxed,
// CGO-free scripting runtime. It is a LEAF package: it imports only
// go.starlark.net and stdlib, never any server/world/level game state, so the
// runtime builds and tests standalone. (Real entity/world exposure is a later
// phase's frozen-handle API.)
package starlark

import (
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// stepBudget bounds a single Thread's execution; a runaway loop hits this and
// returns a *starlark.EvalError ("too many steps") instead of hanging the
// server. Generous default for load-time plugin bodies; a later phase may make
// it per-plugin configurable. (Tests use a far smaller cap so the budget fires
// fast.)
const stepBudget uint64 = 10_000_000

// defaultFileOptions returns the SAFE Starlark dialect via the FileOptions ZERO
// value: recursion CHECK ON (Recursion=false), while-loops OFF, set() OFF, no
// top-level control flow. Do NOT set Recursion:true — that DISABLES the
// recursion guard this runtime requires ON.
func defaultFileOptions() *syntax.FileOptions { return &syntax.FileOptions{} }

// newThread returns a fresh Thread with the step budget set. ONE Thread per
// goroutine — never share a Thread across goroutines (a Thread carries mutable
// per-call state: the step counter + call stack).
func newThread(name string) *starlark.Thread {
	th := &starlark.Thread{Name: name}
	th.SetMaxExecutionSteps(stepBudget)
	return th
}

// safeGlobals is the ENTIRE app-specific surface a plugin can reach. The
// Starlark universe (len/range/dict/...) has NO filesystem/network/eval builtin
// — `open` is undefined — so this allowlist IS the sandbox boundary.
// Server-NEUTRAL only in this phase (real entity/world handles are a later
// phase).
func safeGlobals() starlark.StringDict {
	return starlark.StringDict{
		"echo": starlark.NewBuiltin("echo", echoBuiltin),
	}
}
