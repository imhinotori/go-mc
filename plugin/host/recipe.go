package host

// recipe.go — the value-returning RECIPE seam. This is the Phase-25 dogfood
// point: Phase 22's Emit (emit.go) is VOID dispatch (fire-and-forget); Match
// here is VALUE-RETURNING query-resolution. The ONLY new mechanism is that the
// host READS the matcher's return value (a {id,count} dict) back into Go — that
// single difference proves the plugin API generalizes from event-DISPATCH to a
// 2nd domain (recipe resolution).
//
// Isolation parity with Emit (emit.go callHook): each Match/Remaining runs on a
// FRESH, budget-bounded starlark.Thread (starlarkpkg.NewThread carries the
// Phase-21 SetMaxExecutionSteps) inside a recover — a runaway matcher returns
// *EvalError ("too many steps") and Match returns ok=false; a matcher that
// raises or panics is logged and returns ok=false; the tick never hangs or dies
// (T-25-01 DoS).
//
// Lock-free discipline (same as hooks): recipeMatcher/recipeRemaining/recipeTable
// are WRITTEN only at load (the set_*_matcher builtins capture on the load
// goroutine; SetRecipeTable runs before LoadDir) and READ on the tick goroutine
// (Match/Remaining) — single owner, no locks (TICK-05).

import (
	"log"

	starlarkpkg "github.com/imhinotori/sulfur/plugin/starlark"
	"go.starlark.net/starlark"
)

// makeSetMatcherBuiltin builds the set_recipe_matcher(fn) builtin — the
// value-returning twin of register(event, fn). It captures a single Callable
// into m.recipeMatcher (frozen after Load; safe to Call from the tick).
func (m *Manager) makeSetMatcherBuiltin(pluginName string) *starlark.Builtin {
	return starlark.NewBuiltin("set_recipe_matcher", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var fn starlark.Callable
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &fn); err != nil {
			return nil, err
		}
		m.recipeMatcher = fn
		m.recipeOwner = pluginName
		return starlark.None, nil
	})
}

// makeSetRemainingBuiltin builds set_recipe_remaining(fn): captures the per-cell
// getRemainingItems callable (buckets-back; default all-empty).
func (m *Manager) makeSetRemainingBuiltin(pluginName string) *starlark.Builtin {
	return starlark.NewBuiltin("set_recipe_remaining", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var fn starlark.Callable
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &fn); err != nil {
			return nil, err
		}
		m.recipeRemaining = fn
		m.recipeOwner = pluginName
		return starlark.None, nil
	})
}

// makeRecipesBuiltin builds recipes(): returns the Go-parsed recipe table the
// server injects via SetRecipeTable so the plugin can match without parsing JSON
// (Starlark has no json/file builtin by sandbox design). Returns None until the
// server sets it.
func (m *Manager) makeRecipesBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("recipes", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 0); err != nil {
			return nil, err
		}
		if m.recipeTable == nil {
			return starlark.None, nil
		}
		return m.recipeTable, nil
	})
}

// SetRecipeTable is the exported setter the server uses to hand the Go-parsed
// recipe table (a starlark.List of dicts) to the plugin. MUST be called before
// LoadDir (load-time write, lock-free) — the recipes() builtin reads it.
func (m *Manager) SetRecipeTable(v starlark.Value) { m.recipeTable = v }

// HasRecipeMatcher reports whether a matcher was registered (for tests/operator
// tooling).
func (m *Manager) HasRecipeMatcher() bool { return m.recipeMatcher != nil }

// Match runs the plugin matcher over a grid value and reads back the result
// stack. It mirrors Emit's per-call isolation EXACTLY (fresh budget-bounded
// thread + recover) — the one difference is that it READS the return value (the
// whole new seam). ok=false means: no matcher registered, the matcher errored /
// hit the step budget / panicked, or it returned None / a bogus result.
//
// grid is the plugin-facing payload (a frozen value — a list of (id,count)
// tuples + the dims), built by the caller; the host does not interpret it.
func (m *Manager) Match(grid starlark.Value) (id, count int, ok bool) {
	if m.recipeMatcher == nil {
		return 0, 0, false // nil-matcher fast path (mirrors Emit's zero-subscriber path)
	}
	return m.callValue(m.recipeMatcher, "recipe:match", grid)
}

// Remaining runs the plugin's getRemainingItems callable. For the gate recipes
// the remainder is all-empty (defaultCraftingReminder), so a None / absent
// remaining callable is the faithful default — the caller treats ok=false as
// "no leftover items" (an empty remainder), not an error.
func (m *Manager) Remaining(grid starlark.Value) (id, count int, ok bool) {
	if m.recipeRemaining == nil {
		return 0, 0, false
	}
	return m.callValue(m.recipeRemaining, "recipe:remaining", grid)
}

// callValue is the shared value-returning Call: fresh thread + recover, reading
// back a {id,count} dict. It is the value-returning analogue of emit.go's
// callHook.
func (m *Manager) callValue(fn starlark.Callable, name string, grid starlark.Value) (id, count int, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recipe %s panicked: %v", name, r)
			id, count, ok = 0, 0, false
		}
	}()
	th := starlarkpkg.NewThread(name)
	out, err := starlark.Call(th, fn, starlark.Tuple{grid}, nil)
	if err != nil {
		// Includes *EvalError from the step budget (runaway matcher) — T-25-01.
		log.Printf("recipe %s error: %v", name, err)
		return 0, 0, false
	}
	if out == starlark.None {
		return 0, 0, false
	}
	return readResult(out)
}

// readResult converts a matcher's returned value to a Go (id, count). out is a
// starlark dict {"id": int, "count": int}. readResult VALIDATES id>0 && count>0
// (T-25-03 Tampering): a bogus/negative/non-dict result yields ok=false (no
// forged stack).
func readResult(out starlark.Value) (id, count int, ok bool) {
	d, isDict := out.(*starlark.Dict)
	if !isDict {
		return 0, 0, false
	}
	idv, _, _ := d.Get(starlark.String("id"))
	cnv, _, _ := d.Get(starlark.String("count"))
	if idv == nil || cnv == nil {
		return 0, 0, false
	}
	iid, err1 := starlark.AsInt32(idv)
	icn, err2 := starlark.AsInt32(cnv)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if iid <= 0 || icn <= 0 {
		return 0, 0, false
	}
	return iid, icn, true
}
